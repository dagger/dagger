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
	// The container's root filesystem.
	FS *pb.Definition `json:"fs"`

	// Image configuration (env, workdir, etc)
	Config specs.ImageConfig `json:"cfg"`

	// Mount points configured for the container.
	Mounts []ContainerMount `json:"mounts"`

	// Meta is the /dagger filesystem. It will be null if nothing has run yet.
	Meta *pb.Definition `json:"meta"`
}

type ContainerMount struct {
	Source *pb.Definition `json:"source"`
	Target string         `json:"target"`
}

func (mnt ContainerMount) State() (llb.State, error) {
	defop, err := llb.NewDefinitionOp(mnt.Source)
	if err != nil {
		return llb.State{}, err
	}

	return llb.NewState(defop), nil
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

func NewContainer(ctx context.Context, st llb.State, cfg specs.ImageConfig, mounts []ContainerMount, meta *llb.State) (*Container, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	payload := containerIDPayload{
		FS:     def.ToPB(),
		Config: cfg,
		Mounts: mounts,
	}

	if meta != nil {
		metaDef, err := meta.Marshal(ctx)
		if err != nil {
			return nil, err
		}

		payload.Meta = metaDef.ToPB()
	}

	id, err := encodeID(payload)
	if err != nil {
		return nil, err
	}

	return &Container{
		ID: ContainerID(id),
	}, nil
}

// Decode returns all the relevant information an internal Container related
// API should be concerned with.
//
// NB(vito): Having a million return parameters is an anti-pattern, but I've
// found it useful to have the compiler yell at me to ensure all values are
// considered. Putting them in a struct would make it harder to track down the
// call sites and notice ignored fields. Not married to it though.
func (container *Container) Decode() (llb.State, specs.ImageConfig, []ContainerMount, *llb.State, error) {
	if container.ID == "" {
		return llb.Scratch(), specs.ImageConfig{}, nil, nil, nil
	}

	payload, err := container.ID.decode()
	if err != nil {
		return llb.State{}, specs.ImageConfig{}, nil, nil, err
	}

	fsOp, err := llb.NewDefinitionOp(payload.FS)
	if err != nil {
		return llb.State{}, specs.ImageConfig{}, nil, nil, err
	}

	fs := llb.NewState(fsOp)

	var meta *llb.State
	if payload.Meta != nil {
		metaOp, err := llb.NewDefinitionOp(payload.Meta)
		if err != nil {
			return llb.State{}, specs.ImageConfig{}, nil, nil, err
		}

		metaSt := llb.NewState(metaOp)
		meta = &metaSt
	}

	return fs, payload.Config, payload.Mounts, meta, nil
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
			"withMountedDirectory": router.ToResolver(s.withMountedDirectory),
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

	return NewContainer(ctx, llb.Image(addr), imgSpec.Config, nil, nil)
}

func (s *containerSchema) rootfs(ctx *router.Context, parent *Container, args any) (*Directory, error) {
	st, _, _, _, err := parent.Decode()
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
	st, cfg, mounts, _, err := parent.Decode()
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

	for _, mnt := range mounts {
		st, err := mnt.State()
		if err != nil {
			return nil, err
		}

		// TODO(vito): respect SourcePath
		runOpts = append(runOpts, llb.AddMount(mnt.Target, st))
	}

	execSt := st.Run(runOpts...)

	// propagate any changes to the mounts to subsequent containers
	for i, mnt := range mounts {
		execMountSt, err := execSt.GetMount(mnt.Target).Marshal(ctx, llb.Platform(s.platform))
		if err != nil {
			return nil, err
		}

		mounts[i] = ContainerMount{
			Source: execMountSt.ToPB(),
			Target: mnt.Target,
		}
	}

	metaSt := execSt.GetMount(metaMount)

	return NewContainer(ctx, execSt.Root(), cfg, mounts, &metaSt)
}

func (s *containerSchema) exitCode(ctx *router.Context, parent *Container, args any) (*int, error) {
	_, _, _, meta, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	if meta == nil {
		return nil, nil
	}

	file, err := NewFile(ctx, *meta, "exitCode")
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

func (s *containerSchema) stdout(ctx *router.Context, parent *Container, args any) (*File, error) {
	_, _, _, meta, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	if meta == nil {
		return nil, nil
	}

	return NewFile(ctx, *meta, "stdout")
}

func (s *containerSchema) stderr(ctx *router.Context, parent *Container, args any) (*File, error) {
	_, _, _, meta, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	if meta == nil {
		return nil, nil
	}

	return NewFile(ctx, *meta, "stderr")
}

type containerWithWorkdirArgs struct {
	Path string
}

func (s *containerSchema) withWorkdir(ctx *router.Context, parent *Container, args containerWithWorkdirArgs) (*Container, error) {
	st, cfg, mounts, _, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	cfg.WorkingDir = args.Path

	return NewContainer(ctx, st, cfg, mounts, nil)
}

func (s *containerSchema) workdir(ctx *router.Context, parent *Container, args containerWithVariableArgs) (string, error) {
	_, cfg, _, _, err := parent.Decode()
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
	st, cfg, mounts, _, err := parent.Decode()
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

	return NewContainer(ctx, st, cfg, mounts, nil)
}

func (s *containerSchema) variables(ctx *router.Context, parent *Container, args containerWithVariableArgs) ([]string, error) {
	_, cfg, _, _, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	return cfg.Env, nil
}

type containerWithMountedDirectoryArgs struct {
	Path   string
	Source DirectoryID
}

func (s *containerSchema) withMountedDirectory(ctx *router.Context, parent *Container, args containerWithMountedDirectoryArgs) (*Container, error) {
	st, cfg, mounts, _, err := parent.Decode()
	if err != nil {
		return nil, err
	}

	dir := &Directory{ID: args.Source}

	dirSt, dirRel, err := dir.Decode()
	if err != nil {
		return nil, err
	}

	_ = dirRel // TODO(vito): respect this as SourcePath

	dirDef, err := dirSt.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	mounts = append(mounts, ContainerMount{
		Source: dirDef.ToPB(),
		Target: args.Path,
	})

	return NewContainer(ctx, st, cfg, mounts, nil)
}
