package schema

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/containerd/content"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/util/leaseutil"
)

type containerSchema struct {
	*MergedSchemas

	host         *core.Host
	svcs         *core.Services
	ociStore     content.Store
	leaseManager *leaseutil.Manager
}

var _ ExecutableSchema = &containerSchema{}

func (s *containerSchema) Name() string {
	return "container"
}

func (s *containerSchema) SourceModuleName() string {
	return coreModuleName
}

func (s *containerSchema) Schema() string {
	return Container
}

func (s *containerSchema) Resolvers() Resolvers {
	rs := Resolvers{
		"Query": ObjectResolver{
			"container": ToCachedResolver(s.queryCache, s.container),
		},
	}

	ResolveIDable[*core.Container](s.queryCache, s.MergedSchemas, rs, "Container", ObjectResolver{
		"sync":                    ToCachedResolver(s.queryCache, s.sync),
		"from":                    ToCachedResolver(s.queryCache, s.from),
		"build":                   ToCachedResolver(s.queryCache, s.build),
		"rootfs":                  ToCachedResolver(s.queryCache, s.rootfs),
		"pipeline":                ToCachedResolver(s.queryCache, s.pipeline),
		"withRootfs":              ToCachedResolver(s.queryCache, s.withRootfs),
		"file":                    ToCachedResolver(s.queryCache, s.file),
		"directory":               ToCachedResolver(s.queryCache, s.directory),
		"user":                    ToCachedResolver(s.queryCache, s.user),
		"withUser":                ToCachedResolver(s.queryCache, s.withUser),
		"workdir":                 ToCachedResolver(s.queryCache, s.workdir),
		"withWorkdir":             ToCachedResolver(s.queryCache, s.withWorkdir),
		"envVariables":            ToCachedResolver(s.queryCache, s.envVariables),
		"envVariable":             ToCachedResolver(s.queryCache, s.envVariable),
		"withEnvVariable":         ToCachedResolver(s.queryCache, s.withEnvVariable),
		"withSecretVariable":      ToCachedResolver(s.queryCache, s.withSecretVariable),
		"withoutEnvVariable":      ToCachedResolver(s.queryCache, s.withoutEnvVariable),
		"withLabel":               ToCachedResolver(s.queryCache, s.withLabel),
		"label":                   ToCachedResolver(s.queryCache, s.label),
		"labels":                  ToCachedResolver(s.queryCache, s.labels),
		"withoutLabel":            ToCachedResolver(s.queryCache, s.withoutLabel),
		"entrypoint":              ToCachedResolver(s.queryCache, s.entrypoint),
		"withEntrypoint":          ToCachedResolver(s.queryCache, s.withEntrypoint),
		"defaultArgs":             ToCachedResolver(s.queryCache, s.defaultArgs),
		"withDefaultArgs":         ToCachedResolver(s.queryCache, s.withDefaultArgs),
		"mounts":                  ToCachedResolver(s.queryCache, s.mounts),
		"withMountedDirectory":    ToCachedResolver(s.queryCache, s.withMountedDirectory),
		"withMountedFile":         ToCachedResolver(s.queryCache, s.withMountedFile),
		"withMountedTemp":         ToCachedResolver(s.queryCache, s.withMountedTemp),
		"withMountedCache":        ToCachedResolver(s.queryCache, s.withMountedCache),
		"withMountedSecret":       ToCachedResolver(s.queryCache, s.withMountedSecret),
		"withUnixSocket":          ToCachedResolver(s.queryCache, s.withUnixSocket),
		"withoutUnixSocket":       ToCachedResolver(s.queryCache, s.withoutUnixSocket),
		"withoutMount":            ToCachedResolver(s.queryCache, s.withoutMount),
		"withFile":                ToCachedResolver(s.queryCache, s.withFile),
		"withNewFile":             ToCachedResolver(s.queryCache, s.withNewFile),
		"withDirectory":           ToCachedResolver(s.queryCache, s.withDirectory),
		"withExec":                ToCachedResolver(s.queryCache, s.withExec),
		"stdout":                  ToCachedResolver(s.queryCache, s.stdout),
		"stderr":                  ToCachedResolver(s.queryCache, s.stderr),
		"publish":                 ToResolver(s.publish), // XXX(vito): test
		"platform":                ToCachedResolver(s.queryCache, s.platform),
		"export":                  ToResolver(s.export), // XXX(vito): test
		"asTarball":               ToCachedResolver(s.queryCache, s.asTarball),
		"import":                  ToCachedResolver(s.queryCache, s.import_),
		"withRegistryAuth":        ToCachedResolver(s.queryCache, s.withRegistryAuth),
		"withoutRegistryAuth":     ToCachedResolver(s.queryCache, s.withoutRegistryAuth),
		"imageRef":                ToCachedResolver(s.queryCache, s.imageRef),
		"withExposedPort":         ToCachedResolver(s.queryCache, s.withExposedPort),
		"withoutExposedPort":      ToCachedResolver(s.queryCache, s.withoutExposedPort),
		"exposedPorts":            ToCachedResolver(s.queryCache, s.exposedPorts),
		"withServiceBinding":      ToCachedResolver(s.queryCache, s.withServiceBinding),
		"withFocus":               ToCachedResolver(s.queryCache, s.withFocus),
		"withoutFocus":            ToCachedResolver(s.queryCache, s.withoutFocus),
		"shellEndpoint":           ToResolver(s.shellEndpoint), // XXX(vito): test
		"experimentalWithGPU":     ToCachedResolver(s.queryCache, s.withGPU),
		"experimentalWithAllGPUs": ToCachedResolver(s.queryCache, s.withAllGPUs),
	})

	return rs
}

type containerArgs struct {
	ID       core.ContainerID // XXX(vito): should the pointeriness be moved outside?
	Platform *specs.Platform
}

func (s *containerSchema) container(ctx context.Context, parent *core.Query, args containerArgs) (_ *core.Container, rerr error) {
	platform := s.MergedSchemas.platform
	if args.Platform != nil {
		if args.ID != nil {
			return nil, fmt.Errorf("cannot specify both existing container ID and platform")
		}
		platform = *args.Platform
	}
	if args.ID != nil {
		return load(ctx, args.ID, s.MergedSchemas)
	}
	return core.NewContainer(parent.PipelinePath(), platform)
}

func (s *containerSchema) sync(ctx context.Context, parent *core.Container, _ any) (core.ContainerID, error) {
	_, err := parent.Evaluate(ctx, s.bk, s.svcs)
	if err != nil {
		return nil, err
	}
	return resourceid.FromProto[*core.Container](parent.ID()), nil
}

type containerFromArgs struct {
	Address string
}

func (s *containerSchema) from(ctx context.Context, parent *core.Container, args containerFromArgs) (*core.Container, error) {
	return parent.From(ctx, s.bk, args.Address)
}

type containerBuildArgs struct {
	Context    core.DirectoryID
	Dockerfile string
	BuildArgs  []core.BuildArg
	Target     string
	Secrets    []core.SecretID
}

func (s *containerSchema) build(ctx context.Context, parent *core.Container, args containerBuildArgs) (*core.Container, error) {
	dir, err := load(ctx, args.Context, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	secrets := make([]*core.Secret, len(args.Secrets))
	for i, id := range args.Secrets {
		secrets[i], err = load(ctx, id, s.MergedSchemas)
		if err != nil {
			return nil, err
		}
	}
	return parent.Build(
		ctx,
		dir,
		args.Dockerfile,
		args.BuildArgs,
		args.Target,
		secrets,
		s.bk,
		s.svcs,
	)
}

type containerWithRootFSArgs struct {
	Directory core.DirectoryID
}

func (s *containerSchema) withRootfs(ctx context.Context, parent *core.Container, args containerWithRootFSArgs) (*core.Container, error) {
	dir, err := load(ctx, args.Directory, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithRootFS(ctx, dir)
}

type containerPipelineArgs struct {
	Name        string
	Description string
	Labels      []pipeline.Label
}

func (s *containerSchema) pipeline(ctx context.Context, parent *core.Container, args containerPipelineArgs) (*core.Container, error) {
	return parent.WithPipeline(ctx, args.Name, args.Description, args.Labels)
}

func (s *containerSchema) rootfs(ctx context.Context, parent *core.Container, args any) (*core.Directory, error) {
	return parent.RootFS(ctx)
}

type containerExecArgs struct {
	core.ContainerExecOpts
}

func (s *containerSchema) withExec(ctx context.Context, parent *core.Container, args containerExecArgs) (*core.Container, error) {
	return parent.WithExec(ctx, s.bk, s.progSockPath, s.MergedSchemas.platform, args.ContainerExecOpts)
}

func (s *containerSchema) stdout(ctx context.Context, parent *core.Container, _ any) (string, error) {
	return parent.MetaFileContents(ctx, s.bk, s.svcs, s.progSockPath, "stdout")
}

func (s *containerSchema) stderr(ctx context.Context, parent *core.Container, _ any) (string, error) {
	return parent.MetaFileContents(ctx, s.bk, s.svcs, s.progSockPath, "stderr")
}

type containerGpuArgs struct {
	core.ContainerGPUOpts
}

func (s *containerSchema) withGPU(ctx context.Context, parent *core.Container, args containerGpuArgs) (*core.Container, error) {
	return parent.WithGPU(ctx, args.ContainerGPUOpts)
}

func (s *containerSchema) withAllGPUs(ctx context.Context, parent *core.Container, args containerGpuArgs) (*core.Container, error) {
	return parent.WithGPU(ctx, core.ContainerGPUOpts{Devices: []string{"all"}})
}

type containerWithEntrypointArgs struct {
	Args []string
}

func (s *containerSchema) withEntrypoint(ctx context.Context, parent *core.Container, args containerWithEntrypointArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = args.Args
		return cfg
	})
}

func (s *containerSchema) entrypoint(ctx context.Context, parent *core.Container, args containerWithVariableArgs) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Entrypoint, nil
}

type containerWithDefaultArgs struct {
	Args *[]string
}

func (s *containerSchema) withDefaultArgs(ctx context.Context, parent *core.Container, args containerWithDefaultArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		if args.Args == nil {
			cfg.Cmd = []string{}
			return cfg
		}

		cfg.Cmd = *args.Args
		return cfg
	})
}

func (s *containerSchema) defaultArgs(ctx context.Context, parent *core.Container, args any) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Cmd, nil
}

type containerWithUserArgs struct {
	Name string
}

func (s *containerSchema) withUser(ctx context.Context, parent *core.Container, args containerWithUserArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.User = args.Name
		return cfg
	})
}

func (s *containerSchema) user(ctx context.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.User, nil
}

type containerWithWorkdirArgs struct {
	Path string
}

func (s *containerSchema) withWorkdir(ctx context.Context, parent *core.Container, args containerWithWorkdirArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, args.Path)
		return cfg
	})
}

func (s *containerSchema) workdir(ctx context.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.WorkingDir, nil
}

type containerWithVariableArgs struct {
	Name   string
	Value  string
	Expand bool
}

func (s *containerSchema) withEnvVariable(ctx context.Context, parent *core.Container, args containerWithVariableArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		value := args.Value

		if args.Expand {
			value = os.Expand(value, func(k string) string {
				v, _ := core.LookupEnv(cfg.Env, k)
				return v
			})
		}

		cfg.Env = core.AddEnv(cfg.Env, args.Name, value)

		return cfg
	})
}

type containerWithoutVariableArgs struct {
	Name string
}

func (s *containerSchema) withoutEnvVariable(ctx context.Context, parent *core.Container, args containerWithoutVariableArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		newEnv := []string{}

		core.WalkEnv(cfg.Env, func(k, _, env string) {
			if !shell.EqualEnvKeys(k, args.Name) {
				newEnv = append(newEnv, env)
			}
		})

		cfg.Env = newEnv

		return cfg
	})
}

type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (s *containerSchema) envVariables(ctx context.Context, parent *core.Container, args any) ([]EnvVariable, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	vars := make([]EnvVariable, 0, len(cfg.Env))

	core.WalkEnv(cfg.Env, func(k, v, _ string) {
		vars = append(vars, EnvVariable{Name: k, Value: v})
	})

	return vars, nil
}

type containerVariableArgs struct {
	Name string
}

func (s *containerSchema) envVariable(ctx context.Context, parent *core.Container, args containerVariableArgs) (*string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	if val, ok := core.LookupEnv(cfg.Env, args.Name); ok {
		return &val, nil
	}

	return nil, nil
}

type Label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (s *containerSchema) labels(ctx context.Context, parent *core.Container, args any) ([]Label, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	labels := make([]Label, 0, len(cfg.Labels))
	for name, value := range cfg.Labels {
		label := Label{
			Name:  name,
			Value: value,
		}

		labels = append(labels, label)
	}

	return labels, nil
}

type containerLabelArgs struct {
	Name string
}

func (s *containerSchema) label(ctx context.Context, parent *core.Container, args containerLabelArgs) (*string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	if val, ok := cfg.Labels[args.Name]; ok {
		return &val, nil
	}

	return nil, nil
}

type containerWithMountedDirectoryArgs struct {
	Path   string
	Source core.DirectoryID
	Owner  string
}

func (s *containerSchema) withMountedDirectory(ctx context.Context, parent *core.Container, args containerWithMountedDirectoryArgs) (*core.Container, error) {
	dir, err := load(ctx, args.Source, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithMountedDirectory(ctx, s.bk, args.Path, dir, args.Owner, false)
}

type containerPublishArgs struct {
	Address           string
	PlatformVariants  []core.ContainerID
	ForcedCompression core.ImageLayerCompression
	MediaTypes        core.ImageMediaTypes
}

func (s *containerSchema) publish(ctx context.Context, parent *core.Container, args containerPublishArgs) (string, error) {
	variants := make([]*core.Container, len(args.PlatformVariants))
	for i, id := range args.PlatformVariants {
		var err error
		variants[i], err = load(ctx, id, s.MergedSchemas)
		if err != nil {
			return "", err
		}
	}
	return parent.Publish(ctx, s.bk, s.svcs, args.Address, variants, args.ForcedCompression, args.MediaTypes)
}

type containerWithMountedFileArgs struct {
	Path   string
	Source core.FileID
	Owner  string
}

func (s *containerSchema) withMountedFile(ctx context.Context, parent *core.Container, args containerWithMountedFileArgs) (*core.Container, error) {
	file, err := load(ctx, args.Source, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithMountedFile(ctx, s.bk, args.Path, file, args.Owner, false)
}

type containerWithMountedCacheArgs struct {
	Path        string
	Cache       core.CacheVolumeID
	Source      core.DirectoryID
	Concurrency core.CacheSharingMode
	Owner       string
}

func (s *containerSchema) withMountedCache(ctx context.Context, parent *core.Container, args containerWithMountedCacheArgs) (*core.Container, error) {
	var dir *core.Directory
	if args.Source != nil {
		var err error
		dir, err = load(ctx, args.Source, s.MergedSchemas)
		if err != nil {
			return nil, err
		}
	}

	cache, err := load(ctx, args.Cache, s.MergedSchemas)
	if err != nil {
		return nil, err
	}

	return parent.WithMountedCache(ctx, s.bk, args.Path, cache, dir, args.Concurrency, args.Owner)
}

type containerWithMountedTempArgs struct {
	Path string
}

func (s *containerSchema) withMountedTemp(ctx context.Context, parent *core.Container, args containerWithMountedTempArgs) (*core.Container, error) {
	return parent.WithMountedTemp(ctx, args.Path)
}

type containerWithoutMountArgs struct {
	Path string
}

func (s *containerSchema) withoutMount(ctx context.Context, parent *core.Container, args containerWithoutMountArgs) (*core.Container, error) {
	return parent.WithoutMount(ctx, args.Path)
}

func (s *containerSchema) mounts(ctx context.Context, parent *core.Container, _ any) ([]string, error) {
	return parent.MountTargets(ctx)
}

type containerWithLabelArgs struct {
	Name  string
	Value string
}

func (s *containerSchema) withLabel(ctx context.Context, parent *core.Container, args containerWithLabelArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		if cfg.Labels == nil {
			cfg.Labels = make(map[string]string)
		}
		cfg.Labels[args.Name] = args.Value
		return cfg
	})
}

type containerWithoutLabelArgs struct {
	Name string
}

func (s *containerSchema) withoutLabel(ctx context.Context, parent *core.Container, args containerWithoutLabelArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		delete(cfg.Labels, args.Name)
		return cfg
	})
}

type containerDirectoryArgs struct {
	Path string
}

func (s *containerSchema) directory(ctx context.Context, parent *core.Container, args containerDirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, s.bk, s.svcs, args.Path)
}

type containerFileArgs struct {
	Path string
}

func (s *containerSchema) file(ctx context.Context, parent *core.Container, args containerFileArgs) (*core.File, error) {
	return parent.File(ctx, s.bk, s.svcs, args.Path)
}

func absPath(workDir string, containerPath string) string {
	if path.IsAbs(containerPath) {
		return containerPath
	}

	if workDir == "" {
		workDir = "/"
	}

	return path.Join(workDir, containerPath)
}

type containerWithSecretVariableArgs struct {
	Name   string
	Secret core.SecretID
}

func (s *containerSchema) withSecretVariable(ctx context.Context, parent *core.Container, args containerWithSecretVariableArgs) (*core.Container, error) {
	secret, err := load(ctx, args.Secret, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithSecretVariable(ctx, args.Name, secret)
}

type containerWithMountedSecretArgs struct {
	Path   string
	Source core.SecretID
	Owner  string
	Mode   *int
}

func (s *containerSchema) withMountedSecret(ctx context.Context, parent *core.Container, args containerWithMountedSecretArgs) (*core.Container, error) {
	secret, err := load(ctx, args.Source, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithMountedSecret(ctx, s.bk, args.Path, secret, args.Owner, args.Mode)
}

type containerWithDirectoryArgs struct {
	withDirectoryArgs
	Owner string
}

func (s *containerSchema) withDirectory(ctx context.Context, parent *core.Container, args containerWithDirectoryArgs) (*core.Container, error) {
	dir, err := load(ctx, args.Directory, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithDirectory(ctx, s.bk, args.Path, dir, args.CopyFilter, args.Owner)
}

type containerWithFileArgs struct {
	withFileArgs
	Owner string
}

func (s *containerSchema) withFile(ctx context.Context, parent *core.Container, args containerWithFileArgs) (*core.Container, error) {
	file, err := load(ctx, args.Source, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithFile(ctx, s.bk, args.Path, file, args.Permissions, args.Owner)
}

type containerWithNewFileArgs struct {
	withNewFileArgs
	Owner string
}

func (s *containerSchema) withNewFile(ctx context.Context, parent *core.Container, args containerWithNewFileArgs) (*core.Container, error) {
	return parent.WithNewFile(ctx, s.bk, args.Path, []byte(args.Contents), args.Permissions, args.Owner)
}

type containerWithUnixSocketArgs struct {
	Path   string
	Source core.SocketID
	Owner  string
}

func (s *containerSchema) withUnixSocket(ctx context.Context, parent *core.Container, args containerWithUnixSocketArgs) (*core.Container, error) {
	socket, err := load(ctx, args.Source, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.WithUnixSocket(ctx, s.bk, args.Path, socket, args.Owner)
}

type containerWithoutUnixSocketArgs struct {
	Path string
}

func (s *containerSchema) withoutUnixSocket(ctx context.Context, parent *core.Container, args containerWithoutUnixSocketArgs) (*core.Container, error) {
	return parent.WithoutUnixSocket(ctx, args.Path)
}

func (s *containerSchema) platform(ctx context.Context, parent *core.Container, args any) (specs.Platform, error) {
	return parent.Platform, nil
}

type containerExportArgs struct {
	Path              string
	PlatformVariants  []core.ContainerID
	ForcedCompression core.ImageLayerCompression
	MediaTypes        core.ImageMediaTypes
}

func (s *containerSchema) export(ctx context.Context, parent *core.Container, args containerExportArgs) (bool, error) {
	variants := make([]*core.Container, len(args.PlatformVariants))
	for i, id := range args.PlatformVariants {
		var err error
		variants[i], err = load(ctx, id, s.MergedSchemas)
		if err != nil {
			return false, err
		}
	}
	if err := parent.Export(ctx, s.bk, s.svcs, args.Path, variants, args.ForcedCompression, args.MediaTypes); err != nil {
		return false, err
	}

	return true, nil
}

type containerAsTarballArgs struct {
	PlatformVariants  []core.ContainerID
	ForcedCompression core.ImageLayerCompression
	MediaTypes        core.ImageMediaTypes
}

func (s *containerSchema) asTarball(ctx context.Context, parent *core.Container, args containerAsTarballArgs) (*core.File, error) {
	variants := make([]*core.Container, len(args.PlatformVariants))
	for i, id := range args.PlatformVariants {
		var err error
		variants[i], err = load(ctx, id, s.MergedSchemas)
		if err != nil {
			return nil, err
		}
	}
	return parent.AsTarball(ctx, s.bk, s.MergedSchemas.platform, s.svcs, variants, args.ForcedCompression, args.MediaTypes)
}

type containerImportArgs struct {
	Source core.FileID
	Tag    string
}

func (s *containerSchema) import_(ctx context.Context, parent *core.Container, args containerImportArgs) (*core.Container, error) { // nolint:revive
	source, err := load(ctx, args.Source, s.MergedSchemas)
	if err != nil {
		return nil, err
	}
	return parent.Import(
		ctx,
		source,
		args.Tag,
		s.bk,
		s.host,
		s.svcs,
		s.ociStore,
		s.leaseManager,
	)
}

type containerWithRegistryAuthArgs struct {
	Address  string        `json:"address"`
	Username string        `json:"username"`
	Secret   core.SecretID `json:"secret"`
}

func (s *containerSchema) withRegistryAuth(ctx context.Context, parents *core.Container, args containerWithRegistryAuthArgs) (*core.Container, error) {
	secretBytes, err := s.secrets.GetSecret(ctx, args.Secret.String())
	if err != nil {
		return nil, err
	}

	if err := s.auth.AddCredential(args.Address, args.Username, string(secretBytes)); err != nil {
		return nil, err
	}

	return parents, nil
}

type containerWithoutRegistryAuthArgs struct {
	Address string
}

func (s *containerSchema) withoutRegistryAuth(_ context.Context, parents *core.Container, args containerWithoutRegistryAuthArgs) (*core.Container, error) {
	if err := s.auth.RemoveCredential(args.Address); err != nil {
		return nil, err
	}

	return parents, nil
}

func (s *containerSchema) imageRef(ctx context.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
	return parent.ImageRefOrErr(ctx, s.bk)
}

type containerWithServiceBindingArgs struct {
	Service core.ServiceID
	Alias   string
}

func (s *containerSchema) withServiceBinding(ctx context.Context, parent *core.Container, args containerWithServiceBindingArgs) (*core.Container, error) {
	svc, err := load(ctx, args.Service, s.MergedSchemas)
	if err != nil {
		return nil, err
	}

	return parent.WithServiceBinding(ctx, s.svcs, svc, args.Alias)
}

type containerWithExposedPortArgs struct {
	Protocol    core.NetworkProtocol
	Port        int
	Description *string
}

func (s *containerSchema) withExposedPort(ctx context.Context, parent *core.Container, args containerWithExposedPortArgs) (*core.Container, error) {
	return parent.WithExposedPort(core.Port{
		Protocol:    args.Protocol,
		Port:        args.Port,
		Description: args.Description,
	})
}

type containerWithoutExposedPortArgs struct {
	Protocol core.NetworkProtocol
	Port     int
}

func (s *containerSchema) withoutExposedPort(ctx context.Context, parent *core.Container, args containerWithoutExposedPortArgs) (*core.Container, error) {
	return parent.WithoutExposedPort(args.Port, args.Protocol)
}

// NB(vito): we have to use a different type with a regular string Protocol
// field so that the enum mapping works.
type ExposedPort struct {
	Port        int     `json:"port"`
	Protocol    string  `json:"protocol"`
	Description *string `json:"description,omitempty"`
}

func (s *containerSchema) exposedPorts(ctx context.Context, parent *core.Container, args any) ([]ExposedPort, error) {
	// get descriptions from `Container.Ports` (not in the OCI spec)
	ports := make(map[string]ExposedPort, len(parent.Ports))
	for _, p := range parent.Ports {
		ociPort := fmt.Sprintf("%d/%s", p.Port, p.Protocol.Network())
		ports[ociPort] = ExposedPort{
			Port:        p.Port,
			Protocol:    string(p.Protocol),
			Description: p.Description,
		}
	}

	exposedPorts := []ExposedPort{}
	for ociPort := range parent.Config.ExposedPorts {
		p, exists := ports[ociPort]
		if !exists {
			// ignore errors when parsing from OCI
			port, proto, ok := strings.Cut(ociPort, "/")
			if !ok {
				continue
			}
			portNr, err := strconv.Atoi(port)
			if err != nil {
				continue
			}
			p = ExposedPort{
				Port:     portNr,
				Protocol: strings.ToUpper(proto),
			}
		}
		exposedPorts = append(exposedPorts, p)
	}

	return exposedPorts, nil
}

func (s *containerSchema) withFocus(ctx context.Context, parent *core.Container, args any) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = true
	return child, nil
}

func (s *containerSchema) withoutFocus(ctx context.Context, parent *core.Container, args any) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = false
	return child, nil
}

func (s *containerSchema) shellEndpoint(ctx context.Context, parent *core.Container, args any) (string, error) {
	endpoint, handler, err := parent.ShellEndpoint(s.bk, s.progSockPath, s.services)
	if err != nil {
		return "", err
	}

	if err := s.MuxEndpoint(ctx, path.Join("/", endpoint), handler); err != nil {
		return "", err
	}
	return "ws://dagger/" + endpoint, nil
}
