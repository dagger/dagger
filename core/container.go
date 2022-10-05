package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/docker/distribution/reference"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"go.dagger.io/dagger/core/schema"
	"go.dagger.io/dagger/core/shim"
	"go.dagger.io/dagger/router"
)

// Container is a content-addressed container.
type Container struct {
	ID ContainerID `json:"id"`
}

// ContainerAddress is a container image address.
type ContainerAddress string

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

func (container *Container) FS(ctx context.Context) (*Directory, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	st, err := payload.FSState()
	if err != nil {
		return nil, err
	}

	return NewDirectory(ctx, st, "", payload.Platform)
}

func (container *Container) WithFS(ctx context.Context, st llb.State, platform specs.Platform) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	stDef, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	payload.FS = stDef.ToPB()
	payload.Platform = platform

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) WithMountedDirectory(ctx context.Context, target string, source *Directory) (*Container, error) {
	return container.withMounted(ctx, target, source)
}

func (container *Container) WithMountedFile(ctx context.Context, target string, source *File) (*Container, error) {
	return container.withMounted(ctx, target, source)
}

func (container *Container) WithMountedCache(ctx context.Context, target string, source *Directory) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	mount := ContainerMount{
		Target:           target,
		CacheID:          fmt.Sprintf("%s:%s", container.ID, target),
		CacheSharingMode: "shared", // TODO(vito): add param
	}

	if source != nil {
		dirSt, dirRel, dirPlatform, err := source.Decode()
		if err != nil {
			return nil, err
		}

		dirDef, err := dirSt.Marshal(ctx, llb.Platform(dirPlatform))
		if err != nil {
			return nil, err
		}

		mount.Source = dirDef.ToPB()
		mount.SourcePath = dirRel
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

func (container *Container) Directory(ctx context.Context, gw bkgw.Client, dirPath string) (*Directory, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	dirPath = absPath(payload.Config.WorkingDir, dirPath)

	var dir *Directory

	// NB(vito): iterate in reverse order so we'll find deeper mounts first
	for i := len(payload.Mounts) - 1; i >= 0; i-- {
		mnt := payload.Mounts[i]

		if dirPath == mnt.Target || strings.HasPrefix(dirPath, mnt.Target+"/") {
			if mnt.Tmpfs {
				return nil, fmt.Errorf("%s: cannot retrieve directory from tmpfs", dirPath)
			}

			if mnt.CacheID != "" {
				return nil, fmt.Errorf("%s: cannot retrieve directory from cache", dirPath)
			}

			st, err := mnt.SourceState()
			if err != nil {
				return nil, err
			}

			sub := mnt.SourcePath
			if dirPath != mnt.Target {
				// make relative portion relative to the source path
				dirSub := strings.TrimPrefix(dirPath, mnt.Target+"/")
				if dirSub != "" {
					sub = path.Join(sub, dirSub)
				}
			}

			dir, err = NewDirectory(ctx, st, sub, payload.Platform)
			if err != nil {
				return nil, err
			}

			break
		}
	}

	if dir == nil {
		st, err := payload.FSState()
		if err != nil {
			return nil, err
		}

		dir, err = NewDirectory(ctx, st, dirPath, payload.Platform)
		if err != nil {
			return nil, err
		}
	}

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	_, err = dir.Stat(ctx, gw, ".")
	if err != nil {
		return nil, err
	}

	return dir, nil
}

type mountable interface {
	Decode() (llb.State, string, specs.Platform, error)
}

func (container *Container) withMounted(ctx context.Context, target string, source mountable) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	srcSt, srcRel, srcPlatform, err := source.Decode()
	if err != nil {
		return nil, err
	}

	srcDef, err := srcSt.Marshal(ctx, llb.Platform(srcPlatform))
	if err != nil {
		return nil, err
	}

	payload.Mounts = append(payload.Mounts, ContainerMount{
		Source:     srcDef.ToPB(),
		SourcePath: srcRel,
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

func (container *Container) Exec(ctx context.Context, gw bkgw.Client, args []string, opts ContainerExecOpts) (*Container, error) {
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

	if len(cfg.Entrypoint) > 0 {
		args = append(cfg.Entrypoint, args...)
	}

	runOpts := []llb.RunOption{
		// run the command via the shim, hide shim behind custom name
		llb.AddMount(shim.Path, shimSt, llb.SourcePath(shim.Path)),
		llb.Args(append([]string{shim.Path}, args...)),
		llb.WithCustomName(strings.Join(args, " ")),

		// create /dagger mount point for the shim to write to
		llb.AddMount(
			metaMount,
			// because the shim might run as non-root, we need to make a
			// world-writable directory first...
			llb.Scratch().File(llb.Mkdir(metaSourcePath, 0777)),
			// ...and then make it the base of the mount point.
			llb.SourcePath(metaSourcePath)),
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

	st, err := payload.FSState()
	if err != nil {
		return nil, fmt.Errorf("fs state: %w", err)
	}

	execSt := st.Run(runOpts...)

	for i, mnt := range mounts {
		st, err := mnt.SourceState()
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

		mountSt := execSt.AddMount(mnt.Target, st, mountOpts...)

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

	if file != nil {
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
	ref ContainerAddress,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) (ContainerAddress, error) {
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
				"name": string(ref),
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

	_, err = bkClient.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
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

	return ref, nil
}

type containerSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &containerSchema{}

func (s *containerSchema) Name() string {
	return "container"
}

func (s *containerSchema) Schema() string {
	return schema.Container
}

func (s *containerSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"ContainerID":      stringResolver(ContainerID("")),
		"ContainerAddress": stringResolver(ContainerAddress("")),
		"Query": router.ObjectResolver{
			"container": router.ToResolver(s.container),
		},
		"Container": router.ObjectResolver{
			"from":                 router.ToResolver(s.from),
			"rootfs":               router.ToResolver(s.rootfs),
			"directory":            router.ToResolver(s.directory),
			"user":                 router.ToResolver(s.user),
			"withUser":             router.ToResolver(s.withUser),
			"workdir":              router.ToResolver(s.workdir),
			"withWorkdir":          router.ToResolver(s.withWorkdir),
			"variables":            router.ToResolver(s.variables),
			"variable":             router.ToResolver(s.variable),
			"withVariable":         router.ToResolver(s.withVariable),
			"withSecretVariable":   router.ErrResolver(ErrNotImplementedYet),
			"withoutVariable":      router.ToResolver(s.withoutVariable),
			"entrypoint":           router.ToResolver(s.entrypoint),
			"withEntrypoint":       router.ToResolver(s.withEntrypoint),
			"mounts":               router.ToResolver(s.mounts),
			"withMountedDirectory": router.ToResolver(s.withMountedDirectory),
			"withMountedFile":      router.ToResolver(s.withMountedFile),
			"withMountedTemp":      router.ToResolver(s.withMountedTemp),
			"withMountedCache":     router.ToResolver(s.withMountedCache),
			"withMountedSecret":    router.ErrResolver(ErrNotImplementedYet),
			"withoutMount":         router.ToResolver(s.withoutMount),
			"exec":                 router.ToResolver(s.exec),
			"exitCode":             router.ToResolver(s.exitCode),
			"stdout":               router.ToResolver(s.stdout),
			"stderr":               router.ToResolver(s.stderr),
			"publish":              router.ToResolver(s.publish),
		},
	}
}

func (s *containerSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type containerArgs struct {
	ID ContainerID
}

func (s *containerSchema) container(ctx *router.Context, parent any, args containerArgs) (*Container, error) {
	return &Container{
		ID: args.ID,
	}, nil
}

type containerFromArgs struct {
	Address ContainerAddress
}

func (s *containerSchema) from(ctx *router.Context, parent *Container, args containerFromArgs) (*Container, error) {
	addr := string(args.Address)

	refName, err := reference.ParseNormalizedNamed(addr)
	if err != nil {
		return nil, err
	}

	ref := reference.TagNameOnly(refName).String()

	_, cfgBytes, err := s.gw.ResolveImageConfig(ctx, ref, llb.ResolveImageConfigOpt{
		Platform:    &s.platform,
		ResolveMode: llb.ResolveModeDefault.String(),
	})
	if err != nil {
		return nil, err
	}

	var imgSpec specs.Image
	if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
		return nil, err
	}

	ctr, err := parent.WithFS(ctx, llb.Image(addr), s.platform)
	if err != nil {
		return nil, err
	}

	return ctr.UpdateImageConfig(ctx, func(specs.ImageConfig) specs.ImageConfig {
		return imgSpec.Config
	})
}

func (s *containerSchema) rootfs(ctx *router.Context, parent *Container, args any) (*Directory, error) {
	return parent.FS(ctx)
}

type containerExecArgs struct {
	Args []string
	Opts ContainerExecOpts
}

type ContainerExecOpts struct {
	Stdin          *string
	RedirectStdout *string
	RedirectStderr *string
}

func (s *containerSchema) exec(ctx *router.Context, parent *Container, args containerExecArgs) (*Container, error) {
	return parent.Exec(ctx, s.gw, args.Args, args.Opts)
}

func (s *containerSchema) exitCode(ctx *router.Context, parent *Container, args any) (*int, error) {
	return parent.ExitCode(ctx, s.gw)
}

func (s *containerSchema) stdout(ctx *router.Context, parent *Container, args any) (*File, error) {
	return parent.MetaFile(ctx, s.gw, "stdout")
}

func (s *containerSchema) stderr(ctx *router.Context, parent *Container, args any) (*File, error) {
	return parent.MetaFile(ctx, s.gw, "stderr")
}

type containerWithEntrypointArgs struct {
	Args []string
}

func (s *containerSchema) withEntrypoint(ctx *router.Context, parent *Container, args containerWithEntrypointArgs) (*Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = args.Args
		return cfg
	})
}

func (s *containerSchema) entrypoint(ctx *router.Context, parent *Container, args containerWithVariableArgs) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Entrypoint, nil
}

type containerWithUserArgs struct {
	Name string
}

func (s *containerSchema) withUser(ctx *router.Context, parent *Container, args containerWithUserArgs) (*Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.User = args.Name
		return cfg
	})
}

func (s *containerSchema) user(ctx *router.Context, parent *Container, args containerWithVariableArgs) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.User, nil
}

type containerWithWorkdirArgs struct {
	Path string
}

func (s *containerSchema) withWorkdir(ctx *router.Context, parent *Container, args containerWithWorkdirArgs) (*Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, args.Path)
		return cfg
	})
}

func (s *containerSchema) workdir(ctx *router.Context, parent *Container, args containerWithVariableArgs) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.WorkingDir, nil
}

type containerWithVariableArgs struct {
	Name  string
	Value string
}

func (s *containerSchema) withVariable(ctx *router.Context, parent *Container, args containerWithVariableArgs) (*Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		// NB(vito): buildkit handles replacing properly when we do llb.AddEnv, but
		// we want to replace it here anyway because someone might publish the image
		// instead of running it. (there's a test covering this!)
		newEnv := []string{}
		prefix := args.Name + "="
		for _, env := range cfg.Env {
			if !strings.HasPrefix(env, prefix) {
				newEnv = append(newEnv, env)
			}
		}

		newEnv = append(newEnv, fmt.Sprintf("%s=%s", args.Name, args.Value))

		cfg.Env = newEnv

		return cfg
	})
}

type containerWithoutVariableArgs struct {
	Name string
}

func (s *containerSchema) withoutVariable(ctx *router.Context, parent *Container, args containerWithoutVariableArgs) (*Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		removedEnv := []string{}
		prefix := args.Name + "="
		for _, env := range cfg.Env {
			if !strings.HasPrefix(env, prefix) {
				removedEnv = append(removedEnv, env)
			}
		}

		cfg.Env = removedEnv

		return cfg
	})
}

func (s *containerSchema) variables(ctx *router.Context, parent *Container, args any) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Env, nil
}

type containerVariableArgs struct {
	Name string
}

func (s *containerSchema) variable(ctx *router.Context, parent *Container, args containerVariableArgs) (*string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	for _, env := range cfg.Env {
		name, val, ok := strings.Cut(env, "=")
		if ok && name == args.Name {
			return &val, nil
		}
	}

	return nil, nil
}

type containerWithMountedDirectoryArgs struct {
	Path   string
	Source DirectoryID
}

func (s *containerSchema) withMountedDirectory(ctx *router.Context, parent *Container, args containerWithMountedDirectoryArgs) (*Container, error) {
	return parent.WithMountedDirectory(ctx, args.Path, &Directory{ID: args.Source})
}

type containerPublishArgs struct {
	Address ContainerAddress
}

func (s *containerSchema) publish(ctx *router.Context, parent *Container, args containerPublishArgs) (ContainerAddress, error) {
	return parent.Publish(ctx, args.Address, s.bkClient, s.solveOpts, s.solveCh)
}

type containerWithMountedFileArgs struct {
	Path   string
	Source FileID
}

func (s *containerSchema) withMountedFile(ctx *router.Context, parent *Container, args containerWithMountedFileArgs) (*Container, error) {
	return parent.WithMountedFile(ctx, args.Path, &File{ID: args.Source})
}

type containerWithMountedCacheArgs struct {
	Path   string
	Source DirectoryID
}

func (s *containerSchema) withMountedCache(ctx *router.Context, parent *Container, args containerWithMountedCacheArgs) (*Container, error) {
	var dir *Directory
	if args.Source != "" {
		dir = &Directory{ID: args.Source}
	}

	return parent.WithMountedCache(ctx, args.Path, dir)
}

type containerWithMountedTempArgs struct {
	Path string
}

func (s *containerSchema) withMountedTemp(ctx *router.Context, parent *Container, args containerWithMountedTempArgs) (*Container, error) {
	return parent.WithMountedTemp(ctx, args.Path)
}

type containerWithoutMountArgs struct {
	Path string
}

func (s *containerSchema) withoutMount(ctx *router.Context, parent *Container, args containerWithoutMountArgs) (*Container, error) {
	return parent.WithoutMount(ctx, args.Path)
}

func (s *containerSchema) mounts(ctx *router.Context, parent *Container, _ any) ([]string, error) {
	return parent.Mounts(ctx)
}

type containerDirectoryArgs struct {
	Path string
}

func (s *containerSchema) directory(ctx *router.Context, parent *Container, args containerDirectoryArgs) (*Directory, error) {
	return parent.Directory(ctx, s.gw, args.Path)
}
