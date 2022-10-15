package schema

import (
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type containerSchema struct {
	*baseSchema

	host *core.Host
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
			"from":                 router.ToResolver(s.from),
			"build":                router.ToResolver(s.build),
			"fs":                   router.ToResolver(s.fs),
			"withFS":               router.ToResolver(s.withFS),
			"file":                 router.ToResolver(s.file),
			"directory":            router.ToResolver(s.directory),
			"user":                 router.ToResolver(s.user),
			"withUser":             router.ToResolver(s.withUser),
			"workdir":              router.ToResolver(s.workdir),
			"withWorkdir":          router.ToResolver(s.withWorkdir),
			"envVariables":         router.ToResolver(s.envVariables),
			"envVariable":          router.ToResolver(s.envVariable),
			"withEnvVariable":      router.ToResolver(s.withEnvVariable),
			"withSecretVariable":   router.ToResolver(s.withSecretVariable),
			"withoutEnvVariable":   router.ToResolver(s.withoutEnvVariable),
			"entrypoint":           router.ToResolver(s.entrypoint),
			"withEntrypoint":       router.ToResolver(s.withEntrypoint),
			"defaultArgs":          router.ToResolver(s.defaultArgs),
			"withDefaultArgs":      router.ToResolver(s.withDefaultArgs),
			"mounts":               router.ToResolver(s.mounts),
			"withMountedDirectory": router.ToResolver(s.withMountedDirectory),
			"withMountedFile":      router.ToResolver(s.withMountedFile),
			"withMountedTemp":      router.ToResolver(s.withMountedTemp),
			"withMountedCache":     router.ToResolver(s.withMountedCache),
			"withMountedSecret":    router.ToResolver(s.withMountedSecret),
			"withoutMount":         router.ToResolver(s.withoutMount),
			"exec":                 router.ToResolver(s.exec),
			"exitCode":             router.ToResolver(s.exitCode),
			"stdout":               router.ToResolver(s.stdout),
			"stderr":               router.ToResolver(s.stderr),
			"publish":              router.ToResolver(s.publish),
			"platform":             router.ToResolver(s.platform),
			"export":               router.ToResolver(s.export),
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

func (s *containerSchema) container(ctx *router.Context, parent any, args containerArgs) (*core.Container, error) {
	platform := s.baseSchema.platform
	if args.Platform != nil {
		if args.ID != "" {
			return nil, fmt.Errorf("cannot specify both existing container ID and platform")
		}
		platform = *args.Platform
	}
	return core.NewContainer(args.ID, platform)
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
}

func (s *containerSchema) build(ctx *router.Context, parent *core.Container, args containerBuildArgs) (*core.Container, error) {
	return parent.Build(ctx, s.gw, &core.Directory{ID: args.Context}, args.Dockerfile)
}

func (s *containerSchema) withFS(ctx *router.Context, parent *core.Container, arg core.Directory) (*core.Container, error) {
	ctr, err := parent.WithFS(ctx, &arg)
	if err != nil {
		return nil, err
	}

	return ctr, nil
}

func (s *containerSchema) fs(ctx *router.Context, parent *core.Container, args any) (*core.Directory, error) {
	return parent.FS(ctx)
}

type containerExecArgs struct {
	core.ContainerExecOpts
}

func (s *containerSchema) exec(ctx *router.Context, parent *core.Container, args containerExecArgs) (*core.Container, error) {
	return parent.Exec(ctx, s.gw, s.baseSchema.platform, args.ContainerExecOpts)
}

func (s *containerSchema) exitCode(ctx *router.Context, parent *core.Container, args any) (*int, error) {
	return parent.ExitCode(ctx, s.gw)
}

func (s *containerSchema) stdout(ctx *router.Context, parent *core.Container, args any) (*core.File, error) {
	return parent.MetaFile(ctx, s.gw, "stdout")
}

func (s *containerSchema) stderr(ctx *router.Context, parent *core.Container, args any) (*core.File, error) {
	return parent.MetaFile(ctx, s.gw, "stderr")
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
	Name  string
	Value string
}

func (s *containerSchema) withEnvVariable(ctx *router.Context, parent *core.Container, args containerWithVariableArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutEnvVariable(ctx *router.Context, parent *core.Container, args containerWithoutVariableArgs) (*core.Container, error) {
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
	for _, v := range cfg.Env {
		name, value, _ := strings.Cut(v, "=")
		e := EnvVariable{
			Name:  name,
			Value: value,
		}

		vars = append(vars, e)
	}

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
	Source core.DirectoryID
}

func (s *containerSchema) withMountedDirectory(ctx *router.Context, parent *core.Container, args containerWithMountedDirectoryArgs) (*core.Container, error) {
	return parent.WithMountedDirectory(ctx, args.Path, &core.Directory{ID: args.Source})
}

type containerPublishArgs struct {
	Address          string
	PlatformVariants []core.ContainerID
}

func (s *containerSchema) publish(ctx *router.Context, parent *core.Container, args containerPublishArgs) (string, error) {
	return parent.Publish(ctx, args.Address, args.PlatformVariants, s.bkClient, s.solveOpts, s.solveCh)
}

type containerWithMountedFileArgs struct {
	Path   string
	Source core.FileID
}

func (s *containerSchema) withMountedFile(ctx *router.Context, parent *core.Container, args containerWithMountedFileArgs) (*core.Container, error) {
	return parent.WithMountedFile(ctx, args.Path, &core.File{ID: args.Source})
}

type containerWithMountedCacheArgs struct {
	Path   string
	Cache  core.CacheID
	Source core.DirectoryID
}

func (s *containerSchema) withMountedCache(ctx *router.Context, parent *core.Container, args containerWithMountedCacheArgs) (*core.Container, error) {
	var dir *core.Directory
	if args.Source != "" {
		dir = &core.Directory{ID: args.Source}
	}

	return parent.WithMountedCache(ctx, args.Path, args.Cache, dir)
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
	return parent.Mounts(ctx)
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
	return parent.WithSecretVariable(ctx, args.Name, &core.Secret{ID: args.Secret})
}

type containerWithMountedSecretArgs struct {
	Path   string
	Source core.SecretID
}

func (s *containerSchema) withMountedSecret(ctx *router.Context, parent *core.Container, args containerWithMountedSecretArgs) (*core.Container, error) {
	return parent.WithMountedSecret(ctx, args.Path, core.NewSecret(args.Source))
}

func (s *containerSchema) platform(ctx *router.Context, parent *core.Container, args any) (specs.Platform, error) {
	return parent.Platform()
}

type containerExportArgs struct {
	Path             string
	PlatformVariants []core.ContainerID
}

func (s *containerSchema) export(ctx *router.Context, parent *core.Container, args containerExportArgs) (bool, error) {
	if err := parent.Export(ctx, s.host, args.Path, args.PlatformVariants, s.bkClient, s.solveOpts, s.solveCh); err != nil {
		return false, err
	}

	return true, nil
}
