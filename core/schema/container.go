package schema

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/router"
)

type containerSchema struct {
	*baseSchema
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
		"ContainerID":      stringResolver(core.ContainerID("")),
		"ContainerAddress": stringResolver(core.ContainerAddress("")),
		"Query": router.ObjectResolver{
			"container": router.ToResolver(s.container),
		},
		"Container": router.ObjectResolver{
			"from":                 router.ToResolver(s.from),
			"fs":                   router.ToResolver(s.fs),
			"withFS":               router.ToResolver(s.withFS),
			"file":                 router.ToResolver(s.file),
			"directory":            router.ToResolver(s.directory),
			"user":                 router.ToResolver(s.user),
			"withUser":             router.ToResolver(s.withUser),
			"workdir":              router.ToResolver(s.workdir),
			"withWorkdir":          router.ToResolver(s.withWorkdir),
			"variables":            router.ToResolver(s.variables),
			"variable":             router.ToResolver(s.variable),
			"withVariable":         router.ToResolver(s.withVariable),
			"withSecretVariable":   router.ToResolver(s.withSecretVariable),
			"withoutVariable":      router.ToResolver(s.withoutVariable),
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
		},
	}
}

func (s *containerSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type containerArgs struct {
	ID core.ContainerID
}

func (s *containerSchema) container(ctx *router.Context, parent any, args containerArgs) (*core.Container, error) {
	return &core.Container{
		ID: args.ID,
	}, nil
}

type containerFromArgs struct {
	Address core.ContainerAddress
}

func (s *containerSchema) from(ctx *router.Context, parent *core.Container, args containerFromArgs) (*core.Container, error) {
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

	dir, err := core.NewDirectory(ctx, llb.Image(addr), "/", s.platform)
	if err != nil {
		return nil, err
	}
	ctr, err := parent.WithFS(ctx, dir, s.platform)
	if err != nil {
		return nil, err
	}

	return ctr.UpdateImageConfig(ctx, func(specs.ImageConfig) specs.ImageConfig {
		return imgSpec.Config
	})
}

func (s *containerSchema) withFS(ctx *router.Context, parent *core.Container, arg core.Directory) (*core.Container, error) {
	ctr, err := parent.WithFS(ctx, &arg, s.platform)
	if err != nil {
		return nil, err
	}

	return ctr, nil
}

func (s *containerSchema) fs(ctx *router.Context, parent *core.Container, args any) (*core.Directory, error) {
	return parent.FS(ctx)
}

type containerExecArgs struct {
	// Args is optional. If it is nil, we the default args for the image.
	Args *[]string
	Opts core.ContainerExecOpts
}

func (s *containerSchema) exec(ctx *router.Context, parent *core.Container, args containerExecArgs) (*core.Container, error) {
	return parent.Exec(ctx, s.gw, args.Args, args.Opts)
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

func (s *containerSchema) withVariable(ctx *router.Context, parent *core.Container, args containerWithVariableArgs) (*core.Container, error) {
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

func (s *containerSchema) withoutVariable(ctx *router.Context, parent *core.Container, args containerWithoutVariableArgs) (*core.Container, error) {
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

func (s *containerSchema) variables(ctx *router.Context, parent *core.Container, args any) ([]string, error) {
	cfg, err := parent.ImageConfig(ctx)
	if err != nil {
		return nil, err
	}

	return cfg.Env, nil
}

type containerVariableArgs struct {
	Name string
}

func (s *containerSchema) variable(ctx *router.Context, parent *core.Container, args containerVariableArgs) (*string, error) {
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
	Address core.ContainerAddress
}

func (s *containerSchema) publish(ctx *router.Context, parent *core.Container, args containerPublishArgs) (core.ContainerAddress, error) {
	return parent.Publish(ctx, args.Address, s.bkClient, s.solveOpts, s.solveCh)
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
