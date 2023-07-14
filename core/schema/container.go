package schema

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/router"
)

type containerSchema struct {
	*baseSchema

	host     *core.Host
	ociStore content.Store
}

var _ router.ExecutableSchema = &containerSchema{}

func (s *containerSchema) Name() string {
	return "container"
}

func (s *containerSchema) Schema() string {
	return Container
}

func (s *containerSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"ContainerID": stringResolver(core.ContainerID("")),
		"Query": router.ObjectResolver{
			"container": router.ToResolver(s.container),
		},
		"Container": router.ObjectResolver{
			"id":                      router.ToResolver(s.id),
			"sync":                    router.ToResolver(s.sync),
			"from":                    router.ToResolver(s.from),
			"build":                   router.ToResolver(s.build),
			"rootfs":                  router.ToResolver(s.rootfs),
			"pipeline":                router.ToResolver(s.pipeline),
			"fs":                      router.ToResolver(s.rootfs), // deprecated
			"withRootfs":              router.ToResolver(s.withRootfs),
			"withFS":                  router.ToResolver(s.withRootfs), // deprecated
			"file":                    router.ToResolver(s.file),
			"directory":               router.ToResolver(s.directory),
			"user":                    router.ToResolver(s.user),
			"withUser":                router.ToResolver(s.withUser),
			"workdir":                 router.ToResolver(s.workdir),
			"withWorkdir":             router.ToResolver(s.withWorkdir),
			"envVariables":            router.ToResolver(s.envVariables),
			"envVariable":             router.ToResolver(s.envVariable),
			"withEnvVariable":         router.ToResolver(s.withEnvVariable),
			"withSecretVariable":      router.ToResolver(s.withSecretVariable),
			"withPlainSecretVariable": router.ToResolver(s.withPlainSecretVariable),
			"withoutEnvVariable":      router.ToResolver(s.withoutEnvVariable),
			"withLabel":               router.ToResolver(s.withLabel),
			"label":                   router.ToResolver(s.label),
			"labels":                  router.ToResolver(s.labels),
			"withoutLabel":            router.ToResolver(s.withoutLabel),
			"entrypoint":              router.ToResolver(s.entrypoint),
			"withEntrypoint":          router.ToResolver(s.withEntrypoint),
			"defaultArgs":             router.ToResolver(s.defaultArgs),
			"withDefaultArgs":         router.ToResolver(s.withDefaultArgs),
			"mounts":                  router.ToResolver(s.mounts),
			"withMountedDirectory":    router.ToResolver(s.withMountedDirectory),
			"withMountedFile":         router.ToResolver(s.withMountedFile),
			"withMountedTemp":         router.ToResolver(s.withMountedTemp),
			"withMountedCache":        router.ToResolver(s.withMountedCache),
			"withMountedSecret":       router.ToResolver(s.withMountedSecret),
			"withUnixSocket":          router.ToResolver(s.withUnixSocket),
			"withoutUnixSocket":       router.ToResolver(s.withoutUnixSocket),
			"withoutMount":            router.ToResolver(s.withoutMount),
			"withFile":                router.ToResolver(s.withFile),
			"withNewFile":             router.ToResolver(s.withNewFile),
			"withDirectory":           router.ToResolver(s.withDirectory),
			"withExec":                router.ToResolver(s.withExec),
			"exec":                    router.ToResolver(s.withExec), // deprecated
			"exitCode":                router.ToResolver(s.exitCode),
			"stdout":                  router.ToResolver(s.stdout),
			"stderr":                  router.ToResolver(s.stderr),
			"publish":                 router.ToResolver(s.publish),
			"platform":                router.ToResolver(s.platform),
			"export":                  router.ToResolver(s.export),
			"import":                  router.ToResolver(s.import_),
			"withRegistryAuth":        router.ToResolver(s.withRegistryAuth),
			"withoutRegistryAuth":     router.ToResolver(s.withoutRegistryAuth),
			"imageRef":                router.ToResolver(s.imageRef),
			"withExposedPort":         router.ToResolver(s.withExposedPort),
			"withoutExposedPort":      router.ToResolver(s.withoutExposedPort),
			"exposedPorts":            router.ToResolver(s.exposedPorts),
			"hostname":                router.ToResolver(s.hostname),
			"endpoint":                router.ToResolver(s.endpoint),
			"withServiceBinding":      router.ToResolver(s.withServiceBinding),
			"withFocus":               router.ToResolver(s.withFocus),
			"withoutFocus":            router.ToResolver(s.withoutFocus),
		},
	}
}

func (s *containerSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type containerArgs struct {
	ID       core.ContainerID
	Platform *specs.Platform
}

func (s *containerSchema) container(ctx *router.Context, parent *core.Query, args containerArgs) (*core.Container, error) {
	platform := s.baseSchema.platform
	if args.Platform != nil {
		if args.ID != "" {
			return nil, fmt.Errorf("cannot specify both existing container ID and platform")
		}
		platform = *args.Platform
	}

	ctr, err := core.NewContainer(args.ID, parent.PipelinePath(), platform)
	if err != nil {
		return nil, err
	}
	return ctr, err
}

func (s *containerSchema) sync(ctx *router.Context, parent *core.Container, _ any) (core.ContainerID, error) {
	err := parent.Evaluate(ctx.Context, s.gw)
	if err != nil {
		return "", err
	}
	return parent.ID()
}

func (s *containerSchema) id(ctx *router.Context, parent *core.Container, args any) (core.ContainerID, error) {
	return parent.ID()
}

type containerFromArgs struct {
	Address string
}

func (s *containerSchema) from(ctx *router.Context, parent *core.Container, args containerFromArgs) (*core.Container, error) {
	return parent.From(ctx, s.gw, args.Address)
}

type containerBuildArgs struct {
	Context    core.DirectoryID
	Dockerfile string
	BuildArgs  []core.BuildArg
	Target     string
	Secrets    []core.SecretID
}

func (s *containerSchema) build(ctx *router.Context, parent *core.Container, args containerBuildArgs) (*core.Container, error) {
	dir, err := args.Context.ToDirectory()
	if err != nil {
		return nil, err
	}
	return parent.Build(ctx, s.gw, dir, args.Dockerfile, args.BuildArgs, args.Target, args.Secrets)
}

type containerWithRootFSArgs struct {
	ID core.DirectoryID
}

func (s *containerSchema) withRootfs(ctx *router.Context, parent *core.Container, args containerWithRootFSArgs) (*core.Container, error) {
	dir, err := args.ID.ToDirectory()
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

func (s *containerSchema) pipeline(ctx *router.Context, parent *core.Container, args containerPipelineArgs) (*core.Container, error) {
	return parent.WithPipeline(ctx, args.Name, args.Description, args.Labels)
}

func (s *containerSchema) rootfs(ctx *router.Context, parent *core.Container, args any) (*core.Directory, error) {
	return parent.RootFS(ctx)
}

type containerExecArgs struct {
	core.ContainerExecOpts
}

func (s *containerSchema) withExec(ctx *router.Context, parent *core.Container, args containerExecArgs) (*core.Container, error) {
	progSock := &core.Socket{HostPath: s.progSock}
	return parent.WithExec(ctx, s.gw, progSock, s.baseSchema.platform, args.ContainerExecOpts)
}

func (s *containerSchema) withDefaultExec(ctx *router.Context, parent *core.Container) (*core.Container, error) {
	if parent.Meta == nil {
		return s.withExec(ctx, parent, containerExecArgs{})
	}
	return parent, nil
}

func (s *containerSchema) exitCode(ctx *router.Context, parent *core.Container, args any) (int, error) {
	progSock := &core.Socket{HostPath: s.progSock}
	return parent.ExitCode(ctx, s.gw, progSock)
}

func (s *containerSchema) stdout(ctx *router.Context, parent *core.Container, args any) (string, error) {
	progSock := &core.Socket{HostPath: s.progSock}
	return parent.MetaFileContents(ctx, s.gw, progSock, "stdout")
}

func (s *containerSchema) stderr(ctx *router.Context, parent *core.Container, args any) (string, error) {
	progSock := &core.Socket{HostPath: s.progSock}
	return parent.MetaFileContents(ctx, s.gw, progSock, "stderr")
}

type containerWithEntrypointArgs struct {
	Args []string
}

func (s *containerSchema) withEntrypoint(ctx *router.Context, parent *core.Container, args containerWithEntrypointArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = args.Args
		return cfg
	})
}

func (s *containerSchema) entrypoint(ctx *router.Context, parent *core.Container, args containerWithVariableArgs) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Entrypoint, nil
}

type containerWithDefaultArgs struct {
	Args *[]string
}

func (s *containerSchema) withDefaultArgs(ctx *router.Context, parent *core.Container, args containerWithDefaultArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		if args.Args == nil {
			cfg.Cmd = []string{}
			return cfg
		}

		cfg.Cmd = *args.Args
		return cfg
	})
}

func (s *containerSchema) defaultArgs(ctx *router.Context, parent *core.Container, args any) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Cmd, nil
}

type containerWithUserArgs struct {
	Name string
}

func (s *containerSchema) withUser(ctx *router.Context, parent *core.Container, args containerWithUserArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.User = args.Name
		return cfg
	})
}

func (s *containerSchema) user(ctx *router.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.User, nil
}

type containerWithWorkdirArgs struct {
	Path string
}

func (s *containerSchema) withWorkdir(ctx *router.Context, parent *core.Container, args containerWithWorkdirArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, args.Path)
		return cfg
	})
}

func (s *containerSchema) workdir(ctx *router.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
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

func (s *containerSchema) withEnvVariable(ctx *router.Context, parent *core.Container, args containerWithVariableArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutEnvVariable(ctx *router.Context, parent *core.Container, args containerWithoutVariableArgs) (*core.Container, error) {
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

func (s *containerSchema) envVariables(ctx *router.Context, parent *core.Container, args any) ([]EnvVariable, error) {
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

func (s *containerSchema) envVariable(ctx *router.Context, parent *core.Container, args containerVariableArgs) (*string, error) {
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

func (s *containerSchema) labels(ctx *router.Context, parent *core.Container, args any) ([]Label, error) {
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

func (s *containerSchema) label(ctx *router.Context, parent *core.Container, args containerLabelArgs) (*string, error) {
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

func (s *containerSchema) withMountedDirectory(ctx *router.Context, parent *core.Container, args containerWithMountedDirectoryArgs) (*core.Container, error) {
	dir, err := args.Source.ToDirectory()
	if err != nil {
		return nil, err
	}
	return parent.WithMountedDirectory(ctx, s.gw, args.Path, dir, args.Owner)
}

type containerPublishArgs struct {
	Address           string
	PlatformVariants  []core.ContainerID
	ForcedCompression core.ImageLayerCompression
}

func (s *containerSchema) publish(ctx *router.Context, parent *core.Container, args containerPublishArgs) (string, error) {
	return parent.Publish(ctx, args.Address, args.PlatformVariants, args.ForcedCompression, s.bkClient, s.solveOpts, s.solveCh)
}

type containerWithMountedFileArgs struct {
	Path   string
	Source core.FileID
	Owner  string
}

func (s *containerSchema) withMountedFile(ctx *router.Context, parent *core.Container, args containerWithMountedFileArgs) (*core.Container, error) {
	file, err := args.Source.ToFile()
	if err != nil {
		return nil, err
	}
	return parent.WithMountedFile(ctx, s.gw, args.Path, file, args.Owner)
}

type containerWithMountedCacheArgs struct {
	Path        string
	Cache       core.CacheID
	Source      core.DirectoryID
	Concurrency core.CacheSharingMode
	Owner       string
}

func (s *containerSchema) withMountedCache(ctx *router.Context, parent *core.Container, args containerWithMountedCacheArgs) (*core.Container, error) {
	var dir *core.Directory
	if args.Source != "" {
		var err error
		dir, err = args.Source.ToDirectory()
		if err != nil {
			return nil, err
		}
	}

	cache, err := args.Cache.ToCacheVolume()
	if err != nil {
		return nil, err
	}

	return parent.WithMountedCache(ctx, s.gw, args.Path, cache, dir, args.Concurrency, args.Owner)
}

type containerWithMountedTempArgs struct {
	Path string
}

func (s *containerSchema) withMountedTemp(ctx *router.Context, parent *core.Container, args containerWithMountedTempArgs) (*core.Container, error) {
	return parent.WithMountedTemp(ctx, args.Path)
}

type containerWithoutMountArgs struct {
	Path string
}

func (s *containerSchema) withoutMount(ctx *router.Context, parent *core.Container, args containerWithoutMountArgs) (*core.Container, error) {
	return parent.WithoutMount(ctx, args.Path)
}

func (s *containerSchema) mounts(ctx *router.Context, parent *core.Container, _ any) ([]string, error) {
	return parent.MountTargets(ctx)
}

type containerWithLabelArgs struct {
	Name  string
	Value string
}

func (s *containerSchema) withLabel(ctx *router.Context, parent *core.Container, args containerWithLabelArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutLabel(ctx *router.Context, parent *core.Container, args containerWithoutLabelArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		delete(cfg.Labels, args.Name)
		return cfg
	})
}

type containerDirectoryArgs struct {
	Path string
}

func (s *containerSchema) directory(ctx *router.Context, parent *core.Container, args containerDirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, s.gw, args.Path)
}

type containerFileArgs struct {
	Path string
}

func (s *containerSchema) file(ctx *router.Context, parent *core.Container, args containerFileArgs) (*core.File, error) {
	return parent.File(ctx, s.gw, args.Path)
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

func (s *containerSchema) withSecretVariable(ctx *router.Context, parent *core.Container, args containerWithSecretVariableArgs) (*core.Container, error) {
	secret, err := args.Secret.ToSecret()
	if err != nil {
		return nil, err
	}
	return parent.WithSecretVariable(ctx, args.Name, secret)
}

type containerWithPlainSecretVariableArgs struct {
	Name  string
	Value string
}

func (s *containerSchema) withPlainSecretVariable(ctx *router.Context, parent *core.Container, args containerWithPlainSecretVariableArgs) (*core.Container, error) {
	secretID, err := s.secrets.AddSecret(ctx, args.Name, args.Value)
	if err != nil {
		return nil, err
	}

	secret, err := secretID.ToSecret()
	if err != nil {
		return nil, err
	}

	return parent.WithSecretVariable(ctx, args.Name, secret)
}

type containerWithMountedSecretArgs struct {
	Path   string
	Source core.SecretID
	Owner  string
}

func (s *containerSchema) withMountedSecret(ctx *router.Context, parent *core.Container, args containerWithMountedSecretArgs) (*core.Container, error) {
	secret, err := args.Source.ToSecret()
	if err != nil {
		return nil, err
	}
	return parent.WithMountedSecret(ctx, s.gw, args.Path, secret, args.Owner)
}

type containerWithDirectoryArgs struct {
	withDirectoryArgs
	Owner string
}

func (s *containerSchema) withDirectory(ctx *router.Context, parent *core.Container, args containerWithDirectoryArgs) (*core.Container, error) {
	dir, err := args.Directory.ToDirectory()
	if err != nil {
		return nil, err
	}
	return parent.WithDirectory(ctx, s.gw, args.Path, dir, args.CopyFilter, args.Owner)
}

type containerWithFileArgs struct {
	withFileArgs
	Owner string
}

func (s *containerSchema) withFile(ctx *router.Context, parent *core.Container, args containerWithFileArgs) (*core.Container, error) {
	file, err := args.Source.ToFile()
	if err != nil {
		return nil, err
	}
	return parent.WithFile(ctx, s.gw, args.Path, file, args.Permissions, args.Owner)
}

type containerWithNewFileArgs struct {
	withNewFileArgs
	Owner string
}

func (s *containerSchema) withNewFile(ctx *router.Context, parent *core.Container, args containerWithNewFileArgs) (*core.Container, error) {
	return parent.WithNewFile(ctx, s.gw, args.Path, []byte(args.Contents), args.Permissions, args.Owner)
}

type containerWithUnixSocketArgs struct {
	Path   string
	Source core.SocketID
	Owner  string
}

func (s *containerSchema) withUnixSocket(ctx *router.Context, parent *core.Container, args containerWithUnixSocketArgs) (*core.Container, error) {
	socket, err := args.Source.ToSocket()
	if err != nil {
		return nil, err
	}
	return parent.WithUnixSocket(ctx, s.gw, args.Path, socket, args.Owner)
}

type containerWithoutUnixSocketArgs struct {
	Path string
}

func (s *containerSchema) withoutUnixSocket(ctx *router.Context, parent *core.Container, args containerWithoutUnixSocketArgs) (*core.Container, error) {
	return parent.WithoutUnixSocket(ctx, args.Path)
}

func (s *containerSchema) platform(ctx *router.Context, parent *core.Container, args any) (specs.Platform, error) {
	return parent.Platform, nil
}

type containerExportArgs struct {
	Path              string
	PlatformVariants  []core.ContainerID
	ForcedCompression core.ImageLayerCompression
}

func (s *containerSchema) export(ctx *router.Context, parent *core.Container, args containerExportArgs) (bool, error) {
	if err := parent.Export(ctx, s.host, args.Path, args.PlatformVariants, args.ForcedCompression, s.bkClient, s.solveOpts, s.solveCh); err != nil {
		return false, err
	}

	return true, nil
}

type containerImportArgs struct {
	Source core.FileID
	Tag    string
}

func (s *containerSchema) import_(ctx *router.Context, parent *core.Container, args containerImportArgs) (*core.Container, error) { // nolint:revive
	return parent.Import(ctx, s.gw, s.host, args.Source, args.Tag, s.ociStore)
}

type containerWithRegistryAuthArgs struct {
	Address  string        `json:"address"`
	Username string        `json:"username"`
	Secret   core.SecretID `json:"secret"`
}

func (s *containerSchema) withRegistryAuth(ctx *router.Context, parents *core.Container, args containerWithRegistryAuthArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutRegistryAuth(_ *router.Context, parents *core.Container, args containerWithoutRegistryAuthArgs) (*core.Container, error) {
	if err := s.auth.RemoveCredential(args.Address); err != nil {
		return nil, err
	}

	return parents, nil
}

func (s *containerSchema) imageRef(ctx *router.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
	return parent.ImageRefOrErr(ctx, s.gw)
}

func (s *containerSchema) hostname(ctx *router.Context, parent *core.Container, args any) (string, error) {
	if !s.servicesEnabled {
		return "", ErrServicesDisabled
	}

	parent, err := s.withDefaultExec(ctx, parent)
	if err != nil {
		return "", err
	}

	return parent.HostnameOrErr()
}

type containerEndpointArgs struct {
	Port   int
	Scheme string
}

func (s *containerSchema) endpoint(ctx *router.Context, parent *core.Container, args containerEndpointArgs) (string, error) {
	if !s.servicesEnabled {
		return "", ErrServicesDisabled
	}

	parent, err := s.withDefaultExec(ctx, parent)
	if err != nil {
		return "", err
	}

	return parent.Endpoint(args.Port, args.Scheme)
}

type containerWithServiceDependencyArgs struct {
	Service core.ContainerID
	Alias   string
}

func (s *containerSchema) withServiceBinding(ctx *router.Context, parent *core.Container, args containerWithServiceDependencyArgs) (*core.Container, error) {
	if !s.servicesEnabled {
		return nil, ErrServicesDisabled
	}

	svc, err := args.Service.ToContainer()
	if err != nil {
		return nil, err
	}

	svc, err = s.withDefaultExec(ctx, svc)
	if err != nil {
		return nil, err
	}

	return parent.WithServiceBinding(svc, args.Alias)
}

type containerWithExposedPortArgs struct {
	Protocol    core.NetworkProtocol
	Port        int
	Description *string
}

func (s *containerSchema) withExposedPort(ctx *router.Context, parent *core.Container, args containerWithExposedPortArgs) (*core.Container, error) {
	if !s.servicesEnabled {
		return nil, ErrServicesDisabled
	}

	return parent.WithExposedPort(core.ContainerPort{
		Protocol:    args.Protocol,
		Port:        args.Port,
		Description: args.Description,
	})
}

type containerWithoutExposedPortArgs struct {
	Protocol core.NetworkProtocol
	Port     int
}

func (s *containerSchema) withoutExposedPort(ctx *router.Context, parent *core.Container, args containerWithoutExposedPortArgs) (*core.Container, error) {
	if !s.servicesEnabled {
		return nil, ErrServicesDisabled
	}

	return parent.WithoutExposedPort(args.Port, args.Protocol)
}

// NB(vito): we have to use a different type with a regular string Protocol
// field so that the enum mapping works.
type ExposedPort struct {
	Port        int     `json:"port"`
	Protocol    string  `json:"protocol"`
	Description *string `json:"description,omitempty"`
}

func (s *containerSchema) exposedPorts(ctx *router.Context, parent *core.Container, args any) ([]ExposedPort, error) {
	if !s.servicesEnabled {
		return nil, ErrServicesDisabled
	}

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

func (s *containerSchema) withFocus(ctx *router.Context, parent *core.Container, args any) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = true
	return child, nil
}

func (s *containerSchema) withoutFocus(ctx *router.Context, parent *core.Container, args any) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = false
	return child, nil
}
