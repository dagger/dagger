package dagger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/docker/distribution/reference"
	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/frontend/dockerfile/dockerignore"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	"dagger.io/go/dagger/compiler"
)

const (
	daggerignoreFilename = ".daggerignore"
)

// An execution pipeline
type Pipeline struct {
	name   string
	s      Solver
	state  llb.State
	result bkgw.Reference
	out    *Fillable
}

func NewPipeline(name string, s Solver, out *Fillable) *Pipeline {
	return &Pipeline{
		name:  name,
		s:     s,
		state: llb.Scratch(),
		out:   out,
	}
}

func (p *Pipeline) State() llb.State {
	return p.state
}

func (p *Pipeline) Result() bkgw.Reference {
	return p.result
}

func (p *Pipeline) FS() fs.FS {
	return NewBuildkitFS(p.result)
}

func isComponent(v *compiler.Value) bool {
	return v.Get("#compute").Exists()
}

func ops(code ...*compiler.Value) ([]*compiler.Value, error) {
	ops := []*compiler.Value{}
	// 1. Decode 'code' into a single flat array of operations.
	for _, x := range code {
		// 1. attachment array
		if isComponent(x) {
			xops, err := x.Get("#compute").List()
			if err != nil {
				return nil, err
			}
			// 'from' has an executable attached
			ops = append(ops, xops...)
			// 2. individual op
		} else if _, err := x.Get("do").String(); err == nil {
			ops = append(ops, x)
			// 3. op array
		} else if xops, err := x.List(); err == nil {
			ops = append(ops, xops...)
		} else {
			// 4. error
			return nil, fmt.Errorf("not executable: %s", x.SourceUnsafe())
		}
	}
	return ops, nil
}

func Analyze(fn func(*compiler.Value) error, code ...*compiler.Value) error {
	ops, err := ops(code...)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if err := analyzeOp(fn, op); err != nil {
			return err
		}
	}
	return nil
}

func analyzeOp(fn func(*compiler.Value) error, op *compiler.Value) error {
	if err := fn(op); err != nil {
		return err
	}
	do, err := op.Get("do").String()
	if err != nil {
		return err
	}
	switch do {
	case "load", "copy":
		return Analyze(fn, op.Get("from"))
	case "exec":
		return op.Get("mount").RangeStruct(func(dest string, mnt *compiler.Value) error {
			if from := mnt.Get("from"); from.Exists() {
				return Analyze(fn, from)
			}
			return nil
		})
	}
	return nil
}

// x may be:
//   1) a single operation
//   2) an array of operations
//   3) a value with an attached array of operations
func (p *Pipeline) Do(ctx context.Context, code ...*compiler.Value) error {
	ops, err := ops(code...)
	if err != nil {
		return err
	}
	// 2. Execute each operation in sequence
	for idx, op := range ops {
		// If op not concrete, interrupt without error.
		// This allows gradual resolution:
		//    compute what you can compute.. leave the rest incomplete.
		if err := op.IsConcreteR(); err != nil {
			log.
				Ctx(ctx).
				Warn().
				Str("original_cue_error", err.Error()).
				Int("op", idx).
				Msg("pipeline was partially executed because of missing inputs")
			return nil
		}
		p.state, err = p.doOp(ctx, op, p.state)
		if err != nil {
			return err
		}
		// Force a buildkit solve request at each operation,
		// so that errors map to the correct cue path.
		// FIXME: might as well change FS to make every operation
		// synchronous.
		p.result, err = p.s.Solve(ctx, p.state)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) doOp(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	do, err := op.Get("do").String()
	if err != nil {
		return st, err
	}
	switch do {
	case "copy":
		return p.Copy(ctx, op, st)
	case "exec":
		return p.Exec(ctx, op, st)
	case "export":
		return p.Export(ctx, op, st)
	case "fetch-container":
		return p.FetchContainer(ctx, op, st)
	case "push-container":
		return p.PushContainer(ctx, op, st)
	case "fetch-git":
		return p.FetchGit(ctx, op, st)
	case "local":
		return p.Local(ctx, op, st)
	case "load":
		return p.Load(ctx, op, st)
	case "subdir":
		return p.Subdir(ctx, op, st)
	case "docker-build":
		return p.DockerBuild(ctx, op, st)
	case "write-file":
		return p.WriteFile(ctx, op, st)
	case "mkdir":
		return p.Mkdir(ctx, op, st)
	default:
		return st, fmt.Errorf("invalid operation: %s", op.JSON())
	}
}

func (p *Pipeline) vertexNamef(format string, a ...interface{}) string {
	prefix := fmt.Sprintf("@%s@", p.name)
	name := fmt.Sprintf(format, a...)
	return prefix + " " + name
}

func (p *Pipeline) Subdir(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	// FIXME: this could be more optimized by carrying subdir path as metadata,
	//  and using it in copy, load or mount.

	dir, err := op.Get("dir").String()
	if err != nil {
		return st, err
	}
	return st.File(
		llb.Copy(
			st,
			dir,
			"/",
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
			},
		),
		llb.WithCustomName(p.vertexNamef("Subdir %s", dir)),
	), nil
}

func (p *Pipeline) Copy(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	// Decode copy options
	src, err := op.Get("src").String()
	if err != nil {
		return st, err
	}
	dest, err := op.Get("dest").String()
	if err != nil {
		return st, err
	}
	// Execute 'from' in a tmp pipeline, and use the resulting fs
	from := NewPipeline(op.Get("from").Path().String(), p.s, nil)
	if err := from.Do(ctx, op.Get("from")); err != nil {
		return st, err
	}
	fromResult, err := from.Result().ToState()
	if err != nil {
		return st, err
	}
	return st.File(
		llb.Copy(
			fromResult,
			src,
			dest,
			// FIXME: allow more configurable llb options
			// For now we define the following convenience presets:
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
				CreateDestPath:      true,
				AllowWildcard:       true,
			},
		),
		llb.WithCustomName(p.vertexNamef("Copy %s %s", src, dest)),
	), nil
}

func (p *Pipeline) Local(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	dir, err := op.Get("dir").String()
	if err != nil {
		return st, err
	}

	// daggerignore processing
	// buildkit related setup
	daggerignoreState := llb.Local(
		dir,
		llb.SessionID(p.s.SessionID()),
		llb.FollowPaths([]string{daggerignoreFilename}),
		llb.SharedKeyHint(dir+"-"+daggerignoreFilename),
		llb.WithCustomName(p.vertexNamef("Load %s", daggerignoreFilename)),
	)

	var daggerignore []byte

	def, err := p.s.Marshal(ctx, daggerignoreState)
	if err != nil {
		return st, err
	}
	ref, err := p.s.SolveRequest(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return st, err
	}

	// try to read file
	daggerignore, err = ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: daggerignoreFilename,
	})
	// hack for string introspection because !errors.Is(err, os.ErrNotExist) does not work, same for fs
	if err != nil && !strings.Contains(err.Error(), ".daggerignore: no such file or directory") {
		return st, err
	}

	// parse out excludes, works even if file does not exist
	var excludes []string
	excludes, err = dockerignore.ReadAll(bytes.NewBuffer(daggerignore))
	if err != nil {
		return st, fmt.Errorf("%w failed to parse daggerignore", err)
	}

	// FIXME: Remove the `Copy` and use `Local` directly.
	//
	// Copy'ing is a costly operation which should be unnecessary.
	// However, for a mysterious reason, `llb.Local` is returning a different
	// digest at every run, therefore invalidating the cache.
	//
	// By wrapping `llb.Local` inside `llb.Copy`, we get the same digest for
	// the same content.
	return st.File(
		llb.Copy(
			llb.Local(
				dir,
				// llb.FollowPaths(include),
				llb.ExcludePatterns(excludes),
				llb.WithCustomName(p.vertexNamef("Local %s [transfer]", dir)),

				// Without hint, multiple `llb.Local` operations on the
				// same path get a different digest.
				llb.SessionID(p.s.SessionID()),
				llb.SharedKeyHint(dir),
			),
			"/",
			"/",
		),
		llb.WithCustomName(p.vertexNamef("Local %s [copy]", dir)),
	), nil
}

func (p *Pipeline) Exec(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	opts := []llb.RunOption{}
	var cmd struct {
		Args   []string
		Env    map[string]string
		Dir    string
		Always bool
	}

	if err := op.Decode(&cmd); err != nil {
		return st, err
	}
	// args
	opts = append(opts, llb.Args(cmd.Args))
	// dir
	opts = append(opts, llb.Dir(cmd.Dir))
	// env
	for k, v := range cmd.Env {
		opts = append(opts, llb.AddEnv(k, v))
	}
	// always?
	// FIXME: initialize once for an entire compute job, to avoid cache misses
	if cmd.Always {
		cacheBuster, err := randomID(8)
		if err != nil {
			return st, err
		}
		opts = append(opts, llb.AddEnv("DAGGER_CACHEBUSTER", cacheBuster))
	}
	// mounts
	if mounts := op.Lookup("mount"); mounts.Exists() {
		mntOpts, err := p.mountAll(ctx, mounts)
		if err != nil {
			return st, err
		}
		opts = append(opts, mntOpts...)
	}

	// marker for status events
	// FIXME
	args := make([]string, 0, len(cmd.Args))
	for _, a := range cmd.Args {
		args = append(args, fmt.Sprintf("%q", a))
	}
	opts = append(opts, llb.WithCustomName(p.vertexNamef("Exec [%s]", strings.Join(args, ", "))))

	// --> Execute
	return st.Run(opts...).Root(), nil
}

func (p *Pipeline) mountAll(ctx context.Context, mounts *compiler.Value) ([]llb.RunOption, error) {
	opts := []llb.RunOption{}
	err := mounts.RangeStruct(func(dest string, mnt *compiler.Value) error {
		o, err := p.mount(ctx, dest, mnt)
		if err != nil {
			return err
		}
		opts = append(opts, o)
		return nil
	})
	return opts, err
}

func (p *Pipeline) mount(ctx context.Context, dest string, mnt *compiler.Value) (llb.RunOption, error) {
	if s, err := mnt.String(); err == nil {
		// eg. mount: "/foo": "cache"
		switch s {
		case "cache":
			return llb.AddMount(
				dest,
				llb.Scratch(),
				llb.AsPersistentCacheDir(
					mnt.Path().String(),
					llb.CacheMountShared,
				),
			), nil
		case "tmpfs":
			return llb.AddMount(
				dest,
				llb.Scratch(),
				llb.Tmpfs(),
			), nil
		default:
			return nil, fmt.Errorf("invalid mount source: %q", s)
		}
	}
	// eg. mount: "/foo": { from: www.source }
	from := NewPipeline(mnt.Get("from").Path().String(), p.s, nil)
	if err := from.Do(ctx, mnt.Get("from")); err != nil {
		return nil, err
	}
	fromResult, err := from.Result().ToState()
	if err != nil {
		return nil, err
	}
	// possibly construct mount options for LLB from
	var mo []llb.MountOption
	// handle "path" option
	if mp := mnt.Lookup("path"); mp.Exists() {
		mps, err := mp.String()
		if err != nil {
			return nil, err
		}
		mo = append(mo, llb.SourcePath(mps))
	}
	return llb.AddMount(dest, fromResult, mo...), nil
}

func (p *Pipeline) Export(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	source, err := op.Get("source").String()
	if err != nil {
		return st, err
	}
	format, err := op.Get("format").String()
	if err != nil {
		return st, err
	}
	contents, err := fs.ReadFile(p.FS(), source)
	if err != nil {
		return st, fmt.Errorf("export %s: %w", source, err)
	}
	switch format {
	case "string":
		log.
			Ctx(ctx).
			Debug().
			Bytes("contents", contents).
			Msg("exporting string")

		if err := p.out.Fill(string(contents)); err != nil {
			return st, err
		}
	case "json":
		var o interface{}
		o, err := unmarshalAnything(contents, json.Unmarshal)
		if err != nil {
			return st, err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting json")

		if err := p.out.Fill(o); err != nil {
			return st, err
		}
	case "yaml":
		var o interface{}
		o, err := unmarshalAnything(contents, yaml.Unmarshal)
		if err != nil {
			return st, err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting yaml")

		if err := p.out.Fill(o); err != nil {
			return st, err
		}
	default:
		return st, fmt.Errorf("unsupported export format: %q", format)
	}
	return st, nil
}

type unmarshaller func([]byte, interface{}) error

func unmarshalAnything(data []byte, fn unmarshaller) (interface{}, error) {
	// unmarshalling a map into interface{} yields an error:
	// "unsupported Go type for map key (interface {})"
	// we want to attempt to unmarshal to a map[string]interface{} first
	var oMap map[string]interface{}
	if err := fn(data, &oMap); err == nil {
		return oMap, nil
	}

	// If the previous attempt didn't work, we might be facing a scalar (e.g.
	// bool).
	// Try to unmarshal to interface{} directly.
	var o interface{}
	err := fn(data, &o)
	return o, err
}

func (p *Pipeline) Load(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	// Execute 'from' in a tmp pipeline, and use the resulting fs
	from := NewPipeline(op.Get("from").Path().String(), p.s, nil)
	if err := from.Do(ctx, op.Get("from")); err != nil {
		return st, err
	}
	return from.Result().ToState()
}

func (p *Pipeline) FetchContainer(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	rawRef, err := op.Get("ref").String()
	if err != nil {
		return st, err
	}

	ref, err := reference.ParseNormalizedNamed(rawRef)
	if err != nil {
		return st, fmt.Errorf("failed to parse ref %s: %w", rawRef, err)
	}
	// Add the default tag "latest" to a reference if it only has a repo name.
	ref = reference.TagNameOnly(ref)

	st = llb.Image(
		ref.String(),
		llb.WithCustomName(p.vertexNamef("FetchContainer %s", rawRef)),
	)

	// Load image metadata and convert to to LLB.
	// FIXME: metadata MUST be injected back into the gateway result
	// FIXME: there are unhandled sections of the image config
	image, err := p.s.ResolveImageConfig(ctx, ref.String(), llb.ResolveImageConfigOpt{
		LogName: p.vertexNamef("load metadata for %s", ref.String()),
	})
	if err != nil {
		return st, err
	}

	for _, env := range image.Config.Env {
		k, v := parseKeyValue(env)
		st = st.AddEnv(k, v)
	}
	if image.Config.WorkingDir != "" {
		st = st.Dir(image.Config.WorkingDir)
	}
	if image.Config.User != "" {
		st = st.User(image.Config.User)
	}
	return st, nil
}

func parseKeyValue(env string) (string, string) {
	parts := strings.SplitN(env, "=", 2)
	v := ""
	if len(parts) > 1 {
		v = parts[1]
	}

	return parts[0], v
}

func (p *Pipeline) PushContainer(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	rawRef, err := op.Get("ref").String()
	if err != nil {
		return st, err
	}

	ref, err := reference.ParseNormalizedNamed(rawRef)
	if err != nil {
		return st, fmt.Errorf("failed to parse ref %s: %w", rawRef, err)
	}
	// Add the default tag "latest" to a reference if it only has a repo name.
	ref = reference.TagNameOnly(ref)

	pushSt, err := p.Result().ToState()
	if err != nil {
		return st, err
	}

	_, err = p.s.Export(ctx, pushSt, bk.ExportEntry{
		Type: bk.ExporterImage,
		Attrs: map[string]string{
			"name": ref.String(),
			"push": "true",
		},
	})

	return st, err
}

func (p *Pipeline) FetchGit(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	remote, err := op.Get("remote").String()
	if err != nil {
		return st, err
	}
	ref, err := op.Get("ref").String()
	if err != nil {
		return st, err
	}
	return llb.Git(
		remote,
		ref,
		llb.WithCustomName(p.vertexNamef("FetchGit %s@%s", remote, ref)),
	), nil
}

func (p *Pipeline) DockerBuild(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	var (
		context    = op.Lookup("context")
		dockerfile = op.Lookup("dockerfile")

		contextDef    *bkpb.Definition
		dockerfileDef *bkpb.Definition

		err error
	)

	if !context.Exists() && !dockerfile.Exists() {
		return st, errors.New("context or dockerfile required")
	}

	// docker build context. This can come from another component, so we need to
	// compute it first.
	if context.Exists() {
		from := NewPipeline(op.Lookup("context").Path().String(), p.s, nil)
		if err := from.Do(ctx, context); err != nil {
			return st, err
		}
		fromResult, err := from.Result().ToState()
		if err != nil {
			return st, err
		}
		contextDef, err = p.s.Marshal(ctx, fromResult)
		if err != nil {
			return st, err
		}
		dockerfileDef = contextDef
	}

	// Inlined dockerfile: need to be converted to LLB
	if dockerfile.Exists() {
		content, err := dockerfile.String()
		if err != nil {
			return st, err
		}
		dockerfileDef, err = p.s.Marshal(ctx,
			llb.Scratch().File(
				llb.Mkfile("/Dockerfile", 0644, []byte(content)),
			),
		)
		if err != nil {
			return st, err
		}
		if contextDef == nil {
			contextDef = dockerfileDef
		}
	}

	req := bkgw.SolveRequest{
		Frontend:    "dockerfile.v0",
		FrontendOpt: make(map[string]string),
		FrontendInputs: map[string]*bkpb.Definition{
			dockerfilebuilder.DefaultLocalNameContext:    contextDef,
			dockerfilebuilder.DefaultLocalNameDockerfile: dockerfileDef,
		},
	}

	if dockerfilePath := op.Lookup("dockerfilePath"); dockerfilePath.Exists() {
		filename, err := dockerfilePath.String()
		if err != nil {
			return st, err
		}
		req.FrontendOpt["filename"] = filename
	}

	if buildArgs := op.Lookup("buildArg"); buildArgs.Exists() {
		err := buildArgs.RangeStruct(func(key string, value *compiler.Value) error {
			v, err := value.String()
			if err != nil {
				return err
			}
			req.FrontendOpt["build-arg:"+key] = v
			return nil
		})
		if err != nil {
			return st, err
		}
	}

	if labels := op.Lookup("label"); labels.Exists() {
		err := labels.RangeStruct(func(key string, value *compiler.Value) error {
			s, err := value.String()
			if err != nil {
				return err
			}
			req.FrontendOpt["label:"+key] = s
			return nil
		})
		if err != nil {
			return st, err
		}
	}

	if platforms := op.Lookup("platforms"); platforms.Exists() {
		p := []string{}
		list, err := platforms.List()
		if err != nil {
			return st, err
		}

		for _, platform := range list {
			s, err := platform.String()
			if err != nil {
				return st, err
			}
			p = append(p, s)
		}

		if len(p) > 0 {
			req.FrontendOpt["platform"] = strings.Join(p, ",")
		}
		if len(p) > 1 {
			req.FrontendOpt["multi-platform"] = "true"
		}
	}

	res, err := p.s.SolveRequest(ctx, req)
	if err != nil {
		return st, err
	}
	return res.ToState()
}

func (p *Pipeline) WriteFile(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	content, err := op.Get("content").String()
	if err != nil {
		return st, err
	}

	dest, err := op.Get("dest").String()
	if err != nil {
		return st, err
	}

	mode, err := op.Get("mode").Int64()
	if err != nil {
		return st, err
	}

	return st.File(
		llb.Mkfile(dest, fs.FileMode(mode), []byte(content)),
		llb.WithCustomName(p.vertexNamef("WriteFile %s", dest)),
	), nil
}

func (p *Pipeline) Mkdir(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	path, err := op.Get("path").String()
	if err != nil {
		return st, err
	}

	dir, err := op.Get("dir").String()
	if err != nil {
		return st, err
	}

	mode, err := op.Get("mode").Int64()
	if err != nil {
		return st, err
	}

	return st.Dir(dir).File(
		llb.Mkdir(path, fs.FileMode(mode)),
		llb.WithCustomName(p.vertexNamef("Mkdir %s", path)),
	), nil
}
