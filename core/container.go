package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.dagger.io/dagger/core/schema"
	"go.dagger.io/dagger/core/shim"
	"go.dagger.io/dagger/router"
)

// Container is a content-addressed container.
type Container struct {
	ID ContainerID `json:"id"`
}

// ContainerID is an opaque value representing a content-addressed container.
type ContainerID string

// containerIDPayload is the inner content of a ContainerID.
type containerIDPayload struct {
	FS     *pb.Definition    `json:"fs"`
	Config specs.ImageConfig `json:"cfg"`
	Mounts []ContainerMount  `json:"mounts"`
}

type ContainerMount struct {
	Source *pb.Definition `json:"source"`
	Target string         `json:"target"`
}

// metaMount is the special path that the shim writes metadata to.
const metaMount = "/dagger"

func (id ContainerID) decode() (*containerIDPayload, error) {
	var payload containerIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

// ContainerAddress is a container image address.
type ContainerAddress string

func NewContainer(ctx context.Context, st llb.State, cfg specs.ImageConfig, mounts ...ContainerMount) (*Container, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	id, err := encodeID(containerIDPayload{
		FS:     def.ToPB(),
		Config: cfg,
		Mounts: mounts,
	})
	if err != nil {
		return nil, err
	}

	return &Container{
		ID: ContainerID(id),
	}, nil
}

func (container *Container) Decode() (llb.State, specs.ImageConfig, []ContainerMount, error) {
	if container.ID == "" {
		return llb.Scratch(), specs.ImageConfig{}, nil, nil
	}

	payload, err := container.ID.decode()
	if err != nil {
		return llb.State{}, specs.ImageConfig{}, nil, err
	}

	defop, err := llb.NewDefinitionOp(payload.FS)
	if err != nil {
		return llb.State{}, specs.ImageConfig{}, nil, err
	}

	return llb.NewState(defop), payload.Config, payload.Mounts, nil
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
			"directory":            router.ErrResolver(ErrNotImplementedYet),
			"user":                 router.ErrResolver(ErrNotImplementedYet),
			"withUser":             router.ErrResolver(ErrNotImplementedYet),
			"workdir":              router.ToResolver(s.workdir),
			"withWorkdir":          router.ToResolver(s.withWorkdir),
			"variables":            router.ToResolver(s.variables),
			"variable":             router.ErrResolver(ErrNotImplementedYet),
			"withVariable":         router.ToResolver(s.withVariable),
			"withSecretVariable":   router.ErrResolver(ErrNotImplementedYet),
			"withoutVariable":      router.ErrResolver(ErrNotImplementedYet),
			"entrypoint":           router.ErrResolver(ErrNotImplementedYet),
			"withEntrypoint":       router.ErrResolver(ErrNotImplementedYet),
			"mounts":               router.ErrResolver(ErrNotImplementedYet),
			"withMountedDirectory": router.ErrResolver(ErrNotImplementedYet),
			"withMountedFile":      router.ErrResolver(ErrNotImplementedYet),
			"withMountedTemp":      router.ErrResolver(ErrNotImplementedYet),
			"withMountedCache":     router.ErrResolver(ErrNotImplementedYet),
			"withMountedSecret":    router.ErrResolver(ErrNotImplementedYet),
			"withoutMount":         router.ErrResolver(ErrNotImplementedYet),
			"exec":                 router.ToResolver(s.exec),
			"exitCode":             router.ToResolver(s.exitCode),
			"stdout":               router.ToResolver(s.stdout),
			"stderr":               router.ToResolver(s.stderr),
			"publish":              router.ErrResolver(ErrNotImplementedYet),
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

func (s *containerSchema) from(ctx *router.Context, _ any, args containerFromArgs) (*Container, error) {
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

	return NewContainer(ctx, llb.Image(addr), imgSpec.Config)
}

func (s *containerSchema) rootfs(ctx *router.Context, parent *Container, args any) (*Directory, error) {
	st, _, _, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	return NewDirectory(ctx, st, "")
}

type containerExecArgs struct {
	Args []string
	Opts struct {
		Stdin          *string
		RedirectStdout *string
		RedirectStderr *string
	}
}

func (s *containerSchema) exec(ctx *router.Context, parent *Container, args containerExecArgs) (*Container, error) {
	// TODO(vito): propagate mounts? (or not?)
	st, cfg, _, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	shimSt, err := shim.Build(ctx, s.gw, s.platform)
	if err != nil {
		return nil, err
	}

	runOpts := []llb.RunOption{
		// run the command via the shim, hide shim behind custom name
		llb.AddMount(shim.Path, shimSt, llb.SourcePath(shim.Path)),
		llb.Args(append([]string{shim.Path}, args.Args...)),
		llb.WithCustomName(strings.Join(args.Args, " ")),
		llb.AddMount(metaMount, llb.Scratch()),
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

	execSt := st.Run(runOpts...)

	metaSt, err := execSt.GetMount(metaMount).Marshal(ctx, llb.Platform(s.platform))
	if err != nil {
		return nil, err
	}

	return NewContainer(ctx, execSt.Root(), cfg, ContainerMount{
		Source: metaSt.ToPB(),
		Target: metaMount,
	})
}

func (s *containerSchema) exitCode(ctx *router.Context, parent *Container, args any) (*int, error) {
	_, _, mounts, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	for _, mnt := range mounts {
		if mnt.Target == metaMount {
			defop, err := llb.NewDefinitionOp(mnt.Source)
			if err != nil {
				return nil, err
			}

			file, err := NewFile(ctx, llb.NewState(defop), "exitCode")
			if err != nil {
				return nil, err
			}

			content, err := file.Contents(ctx, s.gw)
			if err != nil {
				return nil, err
			}

			exitCode, err := strconv.Atoi(string(content))
			if err != nil {
				return nil, err
			}

			return &exitCode, nil
		}
	}

	return nil, nil
}

func (s *containerSchema) stdout(ctx *router.Context, parent *Container, args any) (*File, error) {
	_, _, mounts, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	for _, mnt := range mounts {
		if mnt.Target == metaMount {
			defop, err := llb.NewDefinitionOp(mnt.Source)
			if err != nil {
				return nil, err
			}

			return NewFile(ctx, llb.NewState(defop), "stdout")
		}
	}

	return nil, nil
}

func (s *containerSchema) stderr(ctx *router.Context, parent *Container, args any) (*File, error) {
	_, _, mounts, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	for _, mnt := range mounts {
		if mnt.Target == metaMount {
			defop, err := llb.NewDefinitionOp(mnt.Source)
			if err != nil {
				return nil, err
			}

			return NewFile(ctx, llb.NewState(defop), "stderr")
		}
	}

	return nil, nil
}

type containerWithWorkdirArgs struct {
	Path string
}

func (s *containerSchema) withWorkdir(ctx *router.Context, parent *Container, args containerWithWorkdirArgs) (*Container, error) {
	st, cfg, mounts, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	cfg.WorkingDir = args.Path

	return NewContainer(ctx, st, cfg, mounts...)
}

func (s *containerSchema) workdir(ctx *router.Context, parent *Container, args containerWithVariableArgs) (string, error) {
	_, cfg, _, err := parent.Decode()
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
	st, cfg, mounts, err := parent.Decode()
	if err != nil {
		return nil, err
	}

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

	return NewContainer(ctx, st, cfg, mounts...)
}

func (s *containerSchema) variables(ctx *router.Context, parent *Container, args containerWithVariableArgs) ([]string, error) {
	_, cfg, _, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	return cfg.Env, nil
}
