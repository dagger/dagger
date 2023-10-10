package schema

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/containerd/content"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/util/leaseutil"
)

type containerSchema struct {
	*MergedSchemas

	host         *core.Host
	svcs         *core.Services
	ociStore     content.Store
	leaseManager *leaseutil.Manager

	buildCache  *core.CacheMap[uint64, *core.Container]
	importCache *core.CacheMap[uint64, *specs.Descriptor]
}

var _ ExecutableSchema = &containerSchema{}

func (s *containerSchema) Name() string {
	return "container"
}

func (s *containerSchema) Schema() string {
	return Container
}

func (s *containerSchema) Resolvers() Resolvers {
	return Resolvers{
		"ContainerID": stringResolver(core.ContainerID("")),
		"Query": ObjectResolver{
			"container": ToResolver(s.container),
		},
		"Container": ToIDableObjectResolver(core.ContainerID.Decode, ObjectResolver{
			"id":                   ToResolver(s.id),
			"sync":                 ToResolver(s.sync),
			"from":                 ToResolver(s.from),
			"build":                ToResolver(s.build),
			"rootfs":               ToResolver(s.rootfs),
			"pipeline":             ToResolver(s.pipeline),
			"withRootfs":           ToResolver(s.withRootfs),
			"file":                 ToResolver(s.file),
			"directory":            ToResolver(s.directory),
			"user":                 ToResolver(s.user),
			"withUser":             ToResolver(s.withUser),
			"workdir":              ToResolver(s.workdir),
			"withWorkdir":          ToResolver(s.withWorkdir),
			"envVariables":         ToResolver(s.envVariables),
			"envVariable":          ToResolver(s.envVariable),
			"withEnvVariable":      ToResolver(s.withEnvVariable),
			"withSecretVariable":   ToResolver(s.withSecretVariable),
			"withoutEnvVariable":   ToResolver(s.withoutEnvVariable),
			"withLabel":            ToResolver(s.withLabel),
			"label":                ToResolver(s.label),
			"labels":               ToResolver(s.labels),
			"withoutLabel":         ToResolver(s.withoutLabel),
			"entrypoint":           ToResolver(s.entrypoint),
			"withEntrypoint":       ToResolver(s.withEntrypoint),
			"defaultArgs":          ToResolver(s.defaultArgs),
			"withDefaultArgs":      ToResolver(s.withDefaultArgs),
			"mounts":               ToResolver(s.mounts),
			"withMountedDirectory": ToResolver(s.withMountedDirectory),
			"withMountedFile":      ToResolver(s.withMountedFile),
			"withMountedTemp":      ToResolver(s.withMountedTemp),
			"withMountedCache":     ToResolver(s.withMountedCache),
			"withMountedSecret":    ToResolver(s.withMountedSecret),
			"withUnixSocket":       ToResolver(s.withUnixSocket),
			"withoutUnixSocket":    ToResolver(s.withoutUnixSocket),
			"withoutMount":         ToResolver(s.withoutMount),
			"withFile":             ToResolver(s.withFile),
			"withNewFile":          ToResolver(s.withNewFile),
			"withDirectory":        ToResolver(s.withDirectory),
			"withExec":             ToResolver(s.withExec),
			"stdout":               ToResolver(s.stdout),
			"stderr":               ToResolver(s.stderr),
			"publish":              ToResolver(s.publish),
			"platform":             ToResolver(s.platform),
			"export":               ToResolver(s.export),
			"import":               ToResolver(s.import_),
			"withRegistryAuth":     ToResolver(s.withRegistryAuth),
			"withoutRegistryAuth":  ToResolver(s.withoutRegistryAuth),
			"imageRef":             ToResolver(s.imageRef),
			"withExposedPort":      ToResolver(s.withExposedPort),
			"withoutExposedPort":   ToResolver(s.withoutExposedPort),
			"exposedPorts":         ToResolver(s.exposedPorts),
			"withServiceBinding":   ToResolver(s.withServiceBinding),
			"withFocus":            ToResolver(s.withFocus),
			"withoutFocus":         ToResolver(s.withoutFocus),
			"shellEndpoint":        ToResolver(s.shellEndpoint),
		}),
	}
}

func (s *containerSchema) Dependencies() []ExecutableSchema {
	return nil
}

type containerArgs struct {
	ID       core.ContainerID
	Platform *specs.Platform
}

func (s *containerSchema) container(ctx *core.Context, parent *core.Query, args containerArgs) (_ *core.Container, rerr error) {
	if args.ID != "" {
		return args.ID.Decode()
	}
	platform := s.MergedSchemas.platform
	if args.Platform != nil {
		platform = *args.Platform
	}
	ctr, err := core.NewContainer(args.ID, parent.PipelinePath(), platform)
	if err != nil {
		return nil, err
	}
	return ctr, err
}

func (s *containerSchema) sync(ctx *core.Context, parent *core.Container, _ any) (core.ContainerID, error) {
	_, err := parent.Evaluate(ctx, s.bk, s.svcs)
	if err != nil {
		return "", err
	}
	return parent.ID()
}

func (s *containerSchema) id(ctx *core.Context, parent *core.Container, args any) (core.ContainerID, error) {
	return parent.ID()
}

type containerFromArgs struct {
	Address string
}

func (s *containerSchema) from(ctx *core.Context, parent *core.Container, args containerFromArgs) (*core.Container, error) {
	return parent.From(ctx, s.bk, args.Address)
}

type containerBuildArgs struct {
	Context    core.DirectoryID
	Dockerfile string
	BuildArgs  []core.BuildArg
	Target     string
	Secrets    []core.SecretID
}

func (s *containerSchema) build(ctx *core.Context, parent *core.Container, args containerBuildArgs) (*core.Container, error) {
	dir, err := args.Context.Decode()
	if err != nil {
		return nil, err
	}
	return parent.Build(
		ctx,
		dir,
		args.Dockerfile,
		args.BuildArgs,
		args.Target,
		args.Secrets,
		s.bk,
		s.svcs,
		s.buildCache,
	)
}

type containerWithRootFSArgs struct {
	Directory core.DirectoryID
}

func (s *containerSchema) withRootfs(ctx *core.Context, parent *core.Container, args containerWithRootFSArgs) (*core.Container, error) {
	dir, err := args.Directory.Decode()
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

func (s *containerSchema) pipeline(ctx *core.Context, parent *core.Container, args containerPipelineArgs) (*core.Container, error) {
	return parent.WithPipeline(ctx, args.Name, args.Description, args.Labels)
}

func (s *containerSchema) rootfs(ctx *core.Context, parent *core.Container, args any) (*core.Directory, error) {
	return parent.RootFS(ctx)
}

type containerExecArgs struct {
	core.ContainerExecOpts
}

func (s *containerSchema) withExec(ctx *core.Context, parent *core.Container, args containerExecArgs) (*core.Container, error) {
	return parent.WithExec(ctx, s.bk, s.progSockPath, s.MergedSchemas.platform, args.ContainerExecOpts)
}

func (s *containerSchema) stdout(ctx *core.Context, parent *core.Container, _ any) (string, error) {
	return parent.MetaFileContents(ctx, s.bk, s.svcs, s.progSockPath, "stdout")
}

func (s *containerSchema) stderr(ctx *core.Context, parent *core.Container, _ any) (string, error) {
	return parent.MetaFileContents(ctx, s.bk, s.svcs, s.progSockPath, "stderr")
}

type containerWithEntrypointArgs struct {
	Args []string
}

func (s *containerSchema) withEntrypoint(ctx *core.Context, parent *core.Container, args containerWithEntrypointArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = args.Args
		return cfg
	})
}

func (s *containerSchema) entrypoint(ctx *core.Context, parent *core.Container, args containerWithVariableArgs) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Entrypoint, nil
}

type containerWithDefaultArgs struct {
	Args *[]string
}

func (s *containerSchema) withDefaultArgs(ctx *core.Context, parent *core.Container, args containerWithDefaultArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		if args.Args == nil {
			cfg.Cmd = []string{}
			return cfg
		}

		cfg.Cmd = *args.Args
		return cfg
	})
}

func (s *containerSchema) defaultArgs(ctx *core.Context, parent *core.Container, args any) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Cmd, nil
}

type containerWithUserArgs struct {
	Name string
}

func (s *containerSchema) withUser(ctx *core.Context, parent *core.Container, args containerWithUserArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.User = args.Name
		return cfg
	})
}

func (s *containerSchema) user(ctx *core.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return "", err
	}

	return cfg.User, nil
}

type containerWithWorkdirArgs struct {
	Path string
}

func (s *containerSchema) withWorkdir(ctx *core.Context, parent *core.Container, args containerWithWorkdirArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, args.Path)
		return cfg
	})
}

func (s *containerSchema) workdir(ctx *core.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
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

func (s *containerSchema) withEnvVariable(ctx *core.Context, parent *core.Container, args containerWithVariableArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutEnvVariable(ctx *core.Context, parent *core.Container, args containerWithoutVariableArgs) (*core.Container, error) {
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

func (s *containerSchema) envVariables(ctx *core.Context, parent *core.Container, args any) ([]EnvVariable, error) {
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

func (s *containerSchema) envVariable(ctx *core.Context, parent *core.Container, args containerVariableArgs) (*string, error) {
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

func (s *containerSchema) labels(ctx *core.Context, parent *core.Container, args any) ([]Label, error) {
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

func (s *containerSchema) label(ctx *core.Context, parent *core.Container, args containerLabelArgs) (*string, error) {
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

func (s *containerSchema) withMountedDirectory(ctx *core.Context, parent *core.Container, args containerWithMountedDirectoryArgs) (*core.Container, error) {
	dir, err := args.Source.Decode()
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

func (s *containerSchema) publish(ctx *core.Context, parent *core.Container, args containerPublishArgs) (string, error) {
	return parent.Publish(ctx, s.bk, s.svcs, args.Address, args.PlatformVariants, args.ForcedCompression, args.MediaTypes)
}

type containerWithMountedFileArgs struct {
	Path   string
	Source core.FileID
	Owner  string
}

func (s *containerSchema) withMountedFile(ctx *core.Context, parent *core.Container, args containerWithMountedFileArgs) (*core.Container, error) {
	file, err := args.Source.Decode()
	if err != nil {
		return nil, err
	}
	return parent.WithMountedFile(ctx, s.bk, args.Path, file, args.Owner, false)
}

type containerWithMountedCacheArgs struct {
	Path        string
	Cache       core.CacheID
	Source      core.DirectoryID
	Concurrency core.CacheSharingMode
	Owner       string
}

func (s *containerSchema) withMountedCache(ctx *core.Context, parent *core.Container, args containerWithMountedCacheArgs) (*core.Container, error) {
	var dir *core.Directory
	if args.Source != "" {
		var err error
		dir, err = args.Source.Decode()
		if err != nil {
			return nil, err
		}
	}

	cache, err := args.Cache.Decode()
	if err != nil {
		return nil, err
	}

	return parent.WithMountedCache(ctx, s.bk, args.Path, cache, dir, args.Concurrency, args.Owner)
}

type containerWithMountedTempArgs struct {
	Path string
}

func (s *containerSchema) withMountedTemp(ctx *core.Context, parent *core.Container, args containerWithMountedTempArgs) (*core.Container, error) {
	return parent.WithMountedTemp(ctx, args.Path)
}

type containerWithoutMountArgs struct {
	Path string
}

func (s *containerSchema) withoutMount(ctx *core.Context, parent *core.Container, args containerWithoutMountArgs) (*core.Container, error) {
	return parent.WithoutMount(ctx, args.Path)
}

func (s *containerSchema) mounts(ctx *core.Context, parent *core.Container, _ any) ([]string, error) {
	return parent.MountTargets(ctx)
}

type containerWithLabelArgs struct {
	Name  string
	Value string
}

func (s *containerSchema) withLabel(ctx *core.Context, parent *core.Container, args containerWithLabelArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutLabel(ctx *core.Context, parent *core.Container, args containerWithoutLabelArgs) (*core.Container, error) {
	return parent.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		delete(cfg.Labels, args.Name)
		return cfg
	})
}

type containerDirectoryArgs struct {
	Path string
}

func (s *containerSchema) directory(ctx *core.Context, parent *core.Container, args containerDirectoryArgs) (*core.Directory, error) {
	return parent.Directory(ctx, s.bk, s.svcs, args.Path)
}

type containerFileArgs struct {
	Path string
}

func (s *containerSchema) file(ctx *core.Context, parent *core.Container, args containerFileArgs) (*core.File, error) {
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

func (s *containerSchema) withSecretVariable(ctx *core.Context, parent *core.Container, args containerWithSecretVariableArgs) (*core.Container, error) {
	secret, err := args.Secret.Decode()
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

func (s *containerSchema) withMountedSecret(ctx *core.Context, parent *core.Container, args containerWithMountedSecretArgs) (*core.Container, error) {
	secret, err := args.Source.Decode()
	if err != nil {
		return nil, err
	}
	return parent.WithMountedSecret(ctx, s.bk, args.Path, secret, args.Owner, args.Mode)
}

type containerWithDirectoryArgs struct {
	withDirectoryArgs
	Owner string
}

func (s *containerSchema) withDirectory(ctx *core.Context, parent *core.Container, args containerWithDirectoryArgs) (*core.Container, error) {
	dir, err := args.Directory.Decode()
	if err != nil {
		return nil, err
	}
	return parent.WithDirectory(ctx, s.bk, args.Path, dir, args.CopyFilter, args.Owner)
}

type containerWithFileArgs struct {
	withFileArgs
	Owner string
}

func (s *containerSchema) withFile(ctx *core.Context, parent *core.Container, args containerWithFileArgs) (*core.Container, error) {
	file, err := args.Source.Decode()
	if err != nil {
		return nil, err
	}
	return parent.WithFile(ctx, s.bk, args.Path, file, args.Permissions, args.Owner)
}

type containerWithNewFileArgs struct {
	withNewFileArgs
	Owner string
}

func (s *containerSchema) withNewFile(ctx *core.Context, parent *core.Container, args containerWithNewFileArgs) (*core.Container, error) {
	return parent.WithNewFile(ctx, s.bk, args.Path, []byte(args.Contents), args.Permissions, args.Owner)
}

type containerWithUnixSocketArgs struct {
	Path   string
	Source socket.ID
	Owner  string
}

func (s *containerSchema) withUnixSocket(ctx *core.Context, parent *core.Container, args containerWithUnixSocketArgs) (*core.Container, error) {
	socket, err := args.Source.Decode()
	if err != nil {
		return nil, err
	}
	return parent.WithUnixSocket(ctx, s.bk, args.Path, socket, args.Owner)
}

type containerWithoutUnixSocketArgs struct {
	Path string
}

func (s *containerSchema) withoutUnixSocket(ctx *core.Context, parent *core.Container, args containerWithoutUnixSocketArgs) (*core.Container, error) {
	return parent.WithoutUnixSocket(ctx, args.Path)
}

func (s *containerSchema) platform(ctx *core.Context, parent *core.Container, args any) (specs.Platform, error) {
	return parent.Platform, nil
}

type containerExportArgs struct {
	Path              string
	PlatformVariants  []core.ContainerID
	ForcedCompression core.ImageLayerCompression
	MediaTypes        core.ImageMediaTypes
}

func (s *containerSchema) export(ctx *core.Context, parent *core.Container, args containerExportArgs) (bool, error) {
	if err := parent.Export(ctx, s.bk, s.svcs, args.Path, args.PlatformVariants, args.ForcedCompression, args.MediaTypes); err != nil {
		return false, err
	}

	return true, nil
}

type containerImportArgs struct {
	Source core.FileID
	Tag    string
}

func (s *containerSchema) import_(ctx *core.Context, parent *core.Container, args containerImportArgs) (*core.Container, error) { // nolint:revive
	return parent.Import(
		ctx,
		args.Source,
		args.Tag,
		s.bk,
		s.host,
		s.svcs,
		s.importCache,
		s.ociStore,
		s.leaseManager,
	)
}

type containerWithRegistryAuthArgs struct {
	Address  string        `json:"address"`
	Username string        `json:"username"`
	Secret   core.SecretID `json:"secret"`
}

func (s *containerSchema) withRegistryAuth(ctx *core.Context, parents *core.Container, args containerWithRegistryAuthArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutRegistryAuth(_ *core.Context, parents *core.Container, args containerWithoutRegistryAuthArgs) (*core.Container, error) {
	if err := s.auth.RemoveCredential(args.Address); err != nil {
		return nil, err
	}

	return parents, nil
}

func (s *containerSchema) imageRef(ctx *core.Context, parent *core.Container, args containerWithVariableArgs) (string, error) {
	return parent.ImageRefOrErr(ctx, s.bk)
}

type containerWithServiceBindingArgs struct {
	Service core.ServiceID
	Alias   string
}

func (s *containerSchema) withServiceBinding(ctx *core.Context, parent *core.Container, args containerWithServiceBindingArgs) (*core.Container, error) {
	svc, err := args.Service.Decode()
	if err != nil {
		return nil, err
	}

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

func (s *containerSchema) withExposedPort(ctx *core.Context, parent *core.Container, args containerWithExposedPortArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutExposedPort(ctx *core.Context, parent *core.Container, args containerWithoutExposedPortArgs) (*core.Container, error) {
	return parent.WithoutExposedPort(args.Port, args.Protocol)
}

// NB(vito): we have to use a different type with a regular string Protocol
// field so that the enum mapping works.
type ExposedPort struct {
	Port        int     `json:"port"`
	Protocol    string  `json:"protocol"`
	Description *string `json:"description,omitempty"`
}

func (s *containerSchema) exposedPorts(ctx *core.Context, parent *core.Container, args any) ([]ExposedPort, error) {
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

func (s *containerSchema) withFocus(ctx *core.Context, parent *core.Container, args any) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = true
	return child, nil
}

func (s *containerSchema) withoutFocus(ctx *core.Context, parent *core.Container, args any) (*core.Container, error) {
	child := parent.Clone()
	child.Focused = false
	return child, nil
}

func (s *containerSchema) shellEndpoint(ctx *core.Context, parent *core.Container, args any) (string, error) {
	endpoint, handler, err := parent.ShellEndpoint(s.bk, s.progSockPath, s.services)
	if err != nil {
		return "", err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}

	s.MuxEndpoint(path.Join("/", endpoint), handler, clientMetadata.ModuleDigest)
	return "ws://dagger/" + endpoint, nil
}
