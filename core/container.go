package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
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

const (
	DaggerSockName = "dagger-sock"
	DaggerSockPath = "/dagger.sock"
)

// Container is a content-addressed container.
type Container struct {
	ID ContainerID `json:"id"`
}

func NewContainer(id ContainerID, platform specs.Platform) (*Container, error) {
	if id == "" {
		id, err := (&containerIDPayload{Platform: platform}).Encode()
		if err != nil {
			return nil, err
		}
		return &Container{ID: id}, nil
	}
	return &Container{ID: id}, nil
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
	Mounts ContainerMounts `json:"mounts,omitempty"`

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
const metaMount = "/.dagger_meta_mount"

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

type ContainerMounts []ContainerMount

func (mnts ContainerMounts) With(newMnt ContainerMount) ContainerMounts {
	mntsCp := make(ContainerMounts, 0, len(mnts))

	// NB: this / might need to change on Windows, but I'm not even sure how
	// mounts work on Windows, so...
	parent := newMnt.Target + "/"

	for _, mnt := range mnts {
		if mnt.Target == newMnt.Target || strings.HasPrefix(mnt.Target, parent) {
			continue
		}

		mntsCp = append(mntsCp, mnt)
	}

	mntsCp = append(mntsCp, newMnt)

	return mntsCp
}

func (container *Container) From(ctx context.Context, gw bkgw.Client, addr string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}
	platform := payload.Platform

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

	ctr, err := container.WithFS(ctx, dir)
	if err != nil {
		return nil, err
	}

	return ctr.UpdateImageConfig(ctx, func(specs.ImageConfig) specs.ImageConfig {
		return imgSpec.Config
	})
}

const defaultDockerfileName = "Dockerfile"

func (container *Container) Build(ctx context.Context, gw bkgw.Client, context *Directory, dockerfile string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	ctxPayload, err := context.ID.Decode()
	if err != nil {
		return nil, err
	}

	platform := payload.Platform

	opts := map[string]string{
		"platform":      platforms.Format(platform),
		"contextsubdir": ctxPayload.Dir,
	}

	if dockerfile != "" {
		opts["filename"] = filepath.Join(ctxPayload.Dir, dockerfile)
	} else {
		opts["filename"] = filepath.Join(ctxPayload.Dir, defaultDockerfileName)
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

func (container *Container) WithFS(ctx context.Context, dir *Directory) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	dirPayload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	payload.FS = dirPayload.LLB

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

	payload.Mounts = payload.Mounts.With(mount)

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

	payload.Mounts = payload.Mounts.With(ContainerMount{
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

	payload.Mounts = payload.Mounts.With(ContainerMount{
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

	// this allows executed containers to communicate back to this API
	if opts.ExperimentalPrivilegedNesting {
		runOpts = append(runOpts,
			llb.AddEnv("DAGGER_HOST", "unix:///dagger.sock"),
			llb.AddSSHSocket(
				llb.SSHID(DaggerSockName),
				llb.SSHSocketTarget(DaggerSockPath),
			),
		)
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

	for _, mnt := range mounts {
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

		runOpts = append(runOpts, llb.AddMount(mnt.Target, srcSt, mountOpts...))
	}

	execSt := fsSt.Run(runOpts...)

	execDef, err := execSt.Root().Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	payload.FS = execDef.ToPB()

	metaDef, err := execSt.GetMount(metaMount).Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("get meta mount: %w", err)
	}

	payload.Meta = metaDef.ToPB()

	for i, mnt := range mounts {
		if mnt.Tmpfs || mnt.CacheID != "" {
			continue
		}

		mountSt := execSt.GetMount(mnt.Target)

		// propagate any changes to regular mounts to subsequent containers
		execMountDef, err := mountSt.Marshal(ctx, llb.Platform(platform))
		if err != nil {
			return nil, fmt.Errorf("propagate %s: %w", mnt.Target, err)
		}

		mounts[i].Source = execMountDef.ToPB()
	}

	payload.Mounts = mounts

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
	platformVariants []ContainerID,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) (string, error) {
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

	ch, wg := mirrorCh(solveCh)
	defer wg.Wait()

	res, err := bkClient.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		return container.export(ctx, gw, platformVariants)
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

func (container *Container) Platform() (specs.Platform, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return specs.Platform{}, err
	}
	return payload.Platform, nil
}

func (container *Container) Export(
	ctx context.Context,
	host *Host,
	dest string,
	platformVariants []ContainerID,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) error {
	dest, err := host.NormalizeDest(dest)
	if err != nil {
		return err
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}

	defer out.Close()

	return host.Export(ctx, bkclient.ExportEntry{
		Type: bkclient.ExporterOCI,
		Output: func(map[string]string) (io.WriteCloser, error) {
			return out, nil
		},
	}, dest, bkClient, solveOpts, solveCh, func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		return container.export(ctx, gw, platformVariants)
	})
}

func (container *Container) export(
	ctx context.Context,
	gw bkgw.Client,
	platformVariants []ContainerID,
) (*bkgw.Result, error) {
	var payloads []*containerIDPayload
	if container.ID != "" {
		payload, err := container.ID.decode()
		if err != nil {
			return nil, err
		}
		if payload.FS != nil {
			payloads = append(payloads, payload)
		}
	}
	for _, id := range platformVariants {
		payload, err := id.decode()
		if err != nil {
			return nil, err
		}
		if payload.FS != nil {
			payloads = append(payloads, payload)
		}
	}

	if len(payloads) == 0 {
		// Could also just ignore and do nothing, airing on side of error until proven otherwise.
		return nil, errors.New("no containers to export")
	}

	if len(payloads) == 1 {
		payload := payloads[0]

		st, err := payload.FSState()
		if err != nil {
			return nil, err
		}

		stDef, err := st.Marshal(ctx, llb.Platform(payload.Platform))
		if err != nil {
			return nil, err
		}

		res, err := gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: stDef.ToPB(),
		})
		if err != nil {
			return nil, err
		}

		cfgBytes, err := json.Marshal(specs.Image{
			Architecture: payload.Platform.Architecture,
			OS:           payload.Platform.OS,
			OSVersion:    payload.Platform.OSVersion,
			OSFeatures:   payload.Platform.OSFeatures,
			Config:       payload.Config,
		})
		if err != nil {
			return nil, err
		}
		res.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)

		return res, nil
	}

	res := bkgw.NewResult()
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, len(payloads)),
	}

	for i, payload := range payloads {
		st, err := payload.FSState()
		if err != nil {
			return nil, err
		}

		stDef, err := st.Marshal(ctx, llb.Platform(payload.Platform))
		if err != nil {
			return nil, err
		}

		r, err := gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: stDef.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		ref, err := r.SingleRef()
		if err != nil {
			return nil, err
		}

		platformKey := platforms.Format(payload.Platform)
		res.AddRef(platformKey, ref)
		expPlatforms.Platforms[i] = exptypes.Platform{
			ID:       platformKey,
			Platform: payload.Platform,
		}

		cfgBytes, err := json.Marshal(specs.Image{
			Architecture: payload.Platform.Architecture,
			OS:           payload.Platform.OS,
			OSVersion:    payload.Platform.OSVersion,
			OSFeatures:   payload.Platform.OSFeatures,
			Config:       payload.Config,
		})
		if err != nil {
			return nil, err
		}
		res.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformKey), cfgBytes)
	}

	platformBytes, err := json.Marshal(expPlatforms)
	if err != nil {
		return nil, err
	}
	res.AddMeta(exptypes.ExporterPlatformsKey, platformBytes)

	return res, nil
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

	// Provide dagger access to the executed command
	// Do not use this option unless you trust the command being executed.
	// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM
	ExperimentalPrivilegedNesting bool
}
