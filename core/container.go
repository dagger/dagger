package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core/shim"
	"github.com/docker/distribution/reference"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Container is a content-addressed container.
type Container struct {
	ID ContainerID `json:"id"`
}

// ContainerID is an opaque value representing a content-addressed container.
type ContainerID string

func (id ContainerID) decode() (*containerIDPayload, error) {
	if id == "" {
		// scratch
		return &containerIDPayload{}, nil
	}

	var payload containerIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

// containerIDPayload is the inner content of a ContainerID.
type containerIDPayload struct {
	// The container's root filesystem.
	FS *pb.Definition `json:"fs"`

	// Image configuration (env, workdir, etc)
	Config specs.ImageConfig `json:"cfg"`

	// Mount points configured for the container.
	Mounts []ContainerMount `json:"mounts,omitempty"`

	// Meta is the /dagger filesystem. It will be null if nothing has run yet.
	Meta *pb.Definition `json:"meta,omitempty"`

	// The platform of the container's rootfs.
	Platform specs.Platform `json:"platform,omitempty"`

	// Secrets to expose to the container.
	Secrets []ContainerSecret `json:"secret_env,omitempty"`
}

// ContainerSecret configures a secret to expose, either as an environment
// variable or mounted to a file path.
type ContainerSecret struct {
	Secret    SecretID `json:"secret"`
	EnvName   string   `json:"env,omitempty"`
	MountPath string   `json:"path,omitempty"`
}

// Encode returns the opaque string ID representation of the container.
func (payload *containerIDPayload) Encode() (ContainerID, error) {
	id, err := encodeID(payload)
	if err != nil {
		return "", err
	}

	return ContainerID(id), nil
}

// FSState returns the container's root filesystem mount state. If there is
// none (as with an empty container ID), it returns scratch.
func (payload *containerIDPayload) FSState() (llb.State, error) {
	if payload.FS == nil {
		return llb.Scratch(), nil
	}

	return defToState(payload.FS)
}

// metaMount is the special path that the shim writes metadata to.
const metaMount = "/dagger"

// metaSourcePath is a world-writable directory created and mounted to /dagger.
const metaSourcePath = "meta"

// MetaState returns the container's metadata mount state. If the container has
// yet to run, it returns nil.
func (payload *containerIDPayload) MetaState() (*llb.State, error) {
	if payload.Meta == nil {
		return nil, nil
	}

	metaSt, err := defToState(payload.Meta)
	if err != nil {
		return nil, err
	}

	return &metaSt, nil
}

// ContainerMount is a mount point configured in a container.
type ContainerMount struct {
	// The source of the mount.
	Source *pb.Definition `json:"source,omitempty"`

	// A path beneath the source to scope the mount to.
	SourcePath string `json:"source_path,omitempty"`

	// The path of the mount within the container.
	Target string `json:"target"`

	// Persist changes to the mount under this cache ID.
	CacheID string `json:"cache_id,omitempty"`

	// How to share the cache across concurrent runs.
	CacheSharingMode string `json:"cache_sharing,omitempty"`

	// Configure the mount as a tmpfs.
	Tmpfs bool `json:"tmpfs,omitempty"`
}

// SourceState returns the state of the source of the mount.
func (mnt ContainerMount) SourceState() (llb.State, error) {
	if mnt.Source == nil {
		return llb.Scratch(), nil
	}

	return defToState(mnt.Source)
}

func (container *Container) From(ctx context.Context, gw bkgw.Client, addr string, platform specs.Platform) (*Container, error) {
	refName, err := reference.ParseNormalizedNamed(addr)
	if err != nil {
		return nil, err
	}

	ref := reference.TagNameOnly(refName).String()

	_, cfgBytes, err := gw.ResolveImageConfig(ctx, ref, llb.ResolveImageConfigOpt{
		Platform:    &platform,
		ResolveMode: llb.ResolveModeDefault.String(),
	})
	if err != nil {
		return nil, err
	}

	var imgSpec specs.Image
	if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
		return nil, err
	}

	dir, err := NewDirectory(ctx, llb.Image(addr), "/", platform)
	if err != nil {
		return nil, err
	}

	ctr, err := container.WithFS(ctx, dir, platform)
	if err != nil {
		return nil, err
	}

	return ctr.UpdateImageConfig(ctx, func(specs.ImageConfig) specs.ImageConfig {
		return imgSpec.Config
	})
}

func (container *Container) Build(ctx context.Context, gw bkgw.Client, context *Directory, dockerfile string, platform specs.Platform) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	ctxPayload, err := context.ID.Decode()
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		"platform": platforms.Format(platform),
		"filename": dockerfile,
	}

	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    ctxPayload.LLB,
		dockerfilebuilder.DefaultLocalNameDockerfile: ctxPayload.LLB,
	}

	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Frontend:       "dockerfile.v0",
		FrontendOpt:    opts,
		FrontendInputs: inputs,
	})
	if err != nil {
		return nil, err
	}

	bkref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	st, err := bkref.ToState()
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	payload.FS = def.ToPB()
	payload.Platform = platform

	cfgBytes, found := res.Metadata[exptypes.ExporterImageConfigKey]
	if found {
		var imgSpec specs.Image
		if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
			return nil, err
		}

		payload.Config = imgSpec.Config
	}

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) FS(ctx context.Context) (*Directory, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	return (&directoryIDPayload{
		LLB:      payload.FS,
		Platform: payload.Platform,
	}).ToDirectory()
}

func (container *Container) WithFS(ctx context.Context, dir *Directory, platform specs.Platform) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	dirPayload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	payload.FS = dirPayload.LLB
	payload.Platform = platform

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) WithMountedDirectory(ctx context.Context, target string, source *Directory) (*Container, error) {
	payload, err := source.ID.Decode()
	if err != nil {
		return nil, err
	}

	return container.withMounted(target, payload.LLB, payload.Dir)
}

func (container *Container) WithMountedFile(ctx context.Context, target string, source *File) (*Container, error) {
	payload, err := source.ID.decode()
	if err != nil {
		return nil, err
	}

	return container.withMounted(target, payload.LLB, payload.File)
}

func (container *Container) WithMountedCache(ctx context.Context, target string, cache CacheID, source *Directory) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	cachePayload, err := cache.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	mount := ContainerMount{
		Target:           target,
		CacheID:          cachePayload.Sum(),
		CacheSharingMode: "shared", // TODO(vito): add param
	}

	if source != nil {
		srcPayload, err := source.ID.Decode()
		if err != nil {
			return nil, err
		}

		mount.Source = srcPayload.LLB
		mount.SourcePath = srcPayload.Dir
	}

	payload.Mounts = append(payload.Mounts, mount)

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) WithMountedTemp(ctx context.Context, target string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	payload.Mounts = append(payload.Mounts, ContainerMount{
		Target: target,
		Tmpfs:  true,
	})

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) WithMountedSecret(ctx context.Context, target string, source *Secret) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	payload.Secrets = append(payload.Secrets, ContainerSecret{
		Secret:    source.ID,
		MountPath: target,
	})

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) WithoutMount(ctx context.Context, target string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	var found bool
	var foundIdx int
	for i := len(payload.Mounts) - 1; i >= 0; i-- {
		if payload.Mounts[i].Target == target {
			found = true
			foundIdx = i
			break
		}
	}

	if found {
		payload.Mounts = append(payload.Mounts[:foundIdx], payload.Mounts[foundIdx+1:]...)
	}

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) Mounts(ctx context.Context) ([]string, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	mounts := []string{}
	for _, mnt := range payload.Mounts {
		mounts = append(mounts, mnt.Target)
	}

	return mounts, nil
}

func (container *Container) WithSecretVariable(ctx context.Context, name string, secret *Secret) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	payload.Secrets = append(payload.Secrets, ContainerSecret{
		Secret:  secret.ID,
		EnvName: name,
	})

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) Directory(ctx context.Context, gw bkgw.Client, dirPath string) (*Directory, error) {
	dir, err := locatePath(ctx, container, dirPath, gw, NewDirectory)
	if err != nil {
		return nil, err
	}

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	info, err := dir.Stat(ctx, gw, ".")
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is a file, not a directory", dirPath)
	}

	return dir, nil
}

func (container *Container) File(ctx context.Context, gw bkgw.Client, filePath string) (*File, error) {
	file, err := locatePath(ctx, container, filePath, gw, NewFile)
	if err != nil {
		return nil, err
	}

	// check that the file actually exists so the user gets an error earlier
	// rather than when the file is used
	info, err := file.Stat(ctx, gw)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path %s is a directory, not a file", filePath)
	}

	return file, nil
}

func locatePath[T *File | *Directory](
	ctx context.Context,
	container *Container,
	containerPath string,
	gw bkgw.Client,
	init func(context.Context, llb.State, string, specs.Platform) (T, error),
) (T, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	containerPath = absPath(payload.Config.WorkingDir, containerPath)

	var found T

	// NB(vito): iterate in reverse order so we'll find deeper mounts first
	for i := len(payload.Mounts) - 1; i >= 0; i-- {
		mnt := payload.Mounts[i]

		if containerPath == mnt.Target || strings.HasPrefix(containerPath, mnt.Target+"/") {
			if mnt.Tmpfs {
				return nil, fmt.Errorf("%s: cannot retrieve path from tmpfs", containerPath)
			}

			if mnt.CacheID != "" {
				return nil, fmt.Errorf("%s: cannot retrieve path from cache", containerPath)
			}

			st, err := mnt.SourceState()
			if err != nil {
				return nil, err
			}

			sub := mnt.SourcePath
			if containerPath != mnt.Target {
				// make relative portion relative to the source path
				dirSub := strings.TrimPrefix(containerPath, mnt.Target+"/")
				if dirSub != "" {
					sub = path.Join(sub, dirSub)
				}
			}

			found, err = init(ctx, st, sub, payload.Platform)
			if err != nil {
				return nil, err
			}

			break
		}
	}

	if found == nil {
		st, err := payload.FSState()
		if err != nil {
			return nil, err
		}

		found, err = init(ctx, st, containerPath, payload.Platform)
		if err != nil {
			return nil, err
		}
	}

	return found, nil
}

func (container *Container) withMounted(target string, srcDef *pb.Definition, srcPath string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	payload.Mounts = append(payload.Mounts, ContainerMount{
		Source:     srcDef,
		SourcePath: srcPath,
		Target:     target,
	})

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) ImageConfig(ctx context.Context) (specs.ImageConfig, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return specs.ImageConfig{}, err
	}

	return payload.Config, nil
}

func (container *Container) UpdateImageConfig(ctx context.Context, updateFn func(specs.ImageConfig) specs.ImageConfig) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	payload.Config = updateFn(payload.Config)

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) Exec(ctx context.Context, gw bkgw.Client, opts ContainerExecOpts) (*Container, error) { //nolint:gocyclo
	payload, err := container.ID.decode()
	if err != nil {
		return nil, fmt.Errorf("decode id: %w", err)
	}

	cfg := payload.Config
	mounts := payload.Mounts
	platform := payload.Platform

	shimSt, err := shim.Build(ctx, gw, platform)
	if err != nil {
		return nil, fmt.Errorf("build shim: %w", err)
	}

	args := opts.Args

	if len(args) == 0 {
		// we use the default args if no new default args are passed
		args = cfg.Cmd
	}

	if len(cfg.Entrypoint) > 0 {
		args = append(cfg.Entrypoint, args...)
	}

	runOpts := []llb.RunOption{
		// run the command via the shim, hide shim behind custom name
		llb.AddMount(shim.Path, shimSt, llb.SourcePath(shim.Path)),
		llb.Args(append([]string{shim.Path}, args...)),
		llb.WithCustomName(strings.Join(args, " ")),
	}

	// because the shim might run as non-root, we need to make a world-writable
	// directory first and then make it the base of the /dagger mount point.
	//
	// TODO(vito): have the shim exec as the other user instead?
	meta := llb.Mkdir(metaSourcePath, 0777)
	if opts.Stdin != "" {
		meta = meta.Mkfile(path.Join(metaSourcePath, "stdin"), 0600, []byte(opts.Stdin))
	}

	// create /dagger mount point for the shim to write to
	runOpts = append(runOpts,
		llb.AddMount(metaMount,
			llb.Scratch().File(meta),
			llb.SourcePath(metaSourcePath)))

	if opts.RedirectStdout != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDOUT", opts.RedirectStdout))
	}

	if opts.RedirectStderr != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDERR", opts.RedirectStderr))
	}

	if cfg.User != "" {
		runOpts = append(runOpts, llb.User(cfg.User))
	}

	if cfg.WorkingDir != "" {
		runOpts = append(runOpts, llb.Dir(cfg.WorkingDir))
	}

	for _, env := range cfg.Env {
		name, val, ok := strings.Cut(env, "=")
		if !ok {
			// it's OK to not be OK
			// we'll just set an empty env
			_ = ok
		}

		runOpts = append(runOpts, llb.AddEnv(name, val))
	}

	for i, secret := range payload.Secrets {
		secretOpts := []llb.SecretOption{llb.SecretID(string(secret.Secret))}

		var secretDest string
		switch {
		case secret.EnvName != "":
			secretDest = secret.EnvName
			secretOpts = append(secretOpts, llb.SecretAsEnv(true))
		case secret.MountPath != "":
			secretDest = secret.MountPath
		default:
			return nil, fmt.Errorf("malformed secret config at index %d", i)
		}

		runOpts = append(runOpts, llb.AddSecret(secretDest, secretOpts...))
	}

	fsSt, err := payload.FSState()
	if err != nil {
		return nil, fmt.Errorf("fs state: %w", err)
	}

	execSt := fsSt.Run(runOpts...)

	for i, mnt := range mounts {
		srcSt, err := mnt.SourceState()
		if err != nil {
			return nil, fmt.Errorf("mount %s: %w", mnt.Target, err)
		}

		mountOpts := []llb.MountOption{}
		if mnt.SourcePath != "" {
			mountOpts = append(mountOpts, llb.SourcePath(mnt.SourcePath))
		}

		if mnt.CacheSharingMode != "" {
			var sharingMode llb.CacheMountSharingMode
			switch mnt.CacheSharingMode {
			case "shared":
				sharingMode = llb.CacheMountShared
			case "private":
				sharingMode = llb.CacheMountPrivate
			case "locked":
				sharingMode = llb.CacheMountLocked
			default:
				return nil, errors.Errorf("invalid cache mount sharing mode %q", mnt.CacheSharingMode)
			}

			mountOpts = append(mountOpts, llb.AsPersistentCacheDir(mnt.CacheID, sharingMode))
		}

		if mnt.Tmpfs {
			mountOpts = append(mountOpts, llb.Tmpfs())
		}

		mountSt := execSt.AddMount(mnt.Target, srcSt, mountOpts...)

		// propagate any changes to regular mounts to subsequent containers
		if !mnt.Tmpfs && mnt.CacheID == "" {
			execMountDef, err := mountSt.Marshal(ctx, llb.Platform(platform))
			if err != nil {
				return nil, fmt.Errorf("propagate %s: %w", mnt.Target, err)
			}

			mounts[i].Source = execMountDef.ToPB()
		}
	}

	execDef, err := execSt.Root().Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	metaDef, err := execSt.GetMount(metaMount).Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("get meta mount: %w", err)
	}

	payload.FS = execDef.ToPB()
	payload.Mounts = mounts
	payload.Meta = metaDef.ToPB()

	id, err := payload.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return &Container{ID: id}, nil
}

func (container *Container) ExitCode(ctx context.Context, gw bkgw.Client) (*int, error) {
	file, err := container.MetaFile(ctx, gw, "exitCode")
	if err != nil {
		return nil, err
	}

	if file == nil {
		return nil, nil
	}

	content, err := file.Contents(ctx, gw)
	if err != nil {
		return nil, err
	}

	exitCode, err := strconv.Atoi(string(content))
	if err != nil {
		return nil, err
	}

	return &exitCode, nil
}

func (container *Container) MetaFile(ctx context.Context, gw bkgw.Client, filePath string) (*File, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	meta, err := payload.MetaState()
	if err != nil {
		return nil, err
	}

	if meta == nil {
		return nil, nil
	}

	return NewFile(ctx, *meta, path.Join(metaSourcePath, filePath), payload.Platform)
}

func (container *Container) Publish(
	ctx context.Context,
	ref string,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) (string, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return "", err
	}

	st, err := payload.FSState()
	if err != nil {
		return "", err
	}

	stDef, err := st.Marshal(ctx, llb.Platform(payload.Platform))
	if err != nil {
		return "", err
	}

	cfgBytes, err := json.Marshal(specs.Image{
		Architecture: payload.Platform.Architecture,
		OS:           payload.Platform.OS,
		OSVersion:    payload.Platform.OSVersion,
		OSFeatures:   payload.Platform.OSFeatures,
		Config:       payload.Config,
	})
	if err != nil {
		return "", err
	}

	// NOTE: be careful to not overwrite any values from original solveOpts (i.e. with append).
	solveOpts.Exports = []bkclient.ExportEntry{
		{
			Type: bkclient.ExporterImage,
			Attrs: map[string]string{
				"name": ref,
				"push": "true",
			},
		},
	}

	// Mirror events from the sub-Build into the main Build event channel.
	// Build() will close the channel after completion so we don't want to use the main channel directly.
	ch := make(chan *bkclient.SolveStatus)
	go func() {
		for event := range ch {
			solveCh <- event
		}
	}()

	res, err := bkClient.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		res, err := gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: stDef.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		res.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
		return res, nil
	}, ch)
	if err != nil {
		return "", err
	}

	refName, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", err
	}

	imageDigest, found := res.ExporterResponse[exptypes.ExporterImageDigestKey]
	if found {
		dig, err := digest.Parse(imageDigest)
		if err != nil {
			return "", fmt.Errorf("parse digest: %w", err)
		}

		withDig, err := reference.WithDigest(refName, dig)
		if err != nil {
			return "", fmt.Errorf("with digest: %w", err)
		}

		return withDig.String(), nil
	}

	return ref, nil
}

type ContainerExecOpts struct {
	// Command to run instead of the container's default command
	Args []string

	// Content to write to the command's standard input before closing
	Stdin string

	// Redirect the command's standard output to a file in the container
	RedirectStdout string

	// Redirect the command's standard error to a file in the container
	RedirectStderr string
}
