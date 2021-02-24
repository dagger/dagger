package dagger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkpb "github.com/moby/buildkit/solver/pb"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	"dagger.io/go/dagger/compiler"
)

// An execution pipeline
type Pipeline struct {
	name string
	s    Solver
	fs   FS
	out  *Fillable
}

func NewPipeline(name string, s Solver, out *Fillable) *Pipeline {
	return &Pipeline{
		name: name,
		s:    s,
		fs:   s.Scratch(),
		out:  out,
	}
}

func (p *Pipeline) FS() FS {
	return p.fs
}

func isComponent(v *compiler.Value) bool {
	return v.Get("#dagger.compute").Exists()
}

func ops(code ...*compiler.Value) ([]*compiler.Value, error) {
	ops := []*compiler.Value{}
	// 1. Decode 'code' into a single flat array of operations.
	for _, x := range code {
		// 1. attachment array
		if isComponent(x) {
			xops, err := x.Get("#dagger.compute").List()
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
		if err := p.doOp(ctx, op); err != nil {
			return err
		}
		// Force a buildkit solve request at each operation,
		// so that errors map to the correct cue path.
		// FIXME: might as well change FS to make every operation
		// synchronous.
		fs, err := p.fs.Solve(ctx)
		if err != nil {
			return err
		}
		p.fs = fs
	}
	return nil
}

func (p *Pipeline) doOp(ctx context.Context, op *compiler.Value) error {
	do, err := op.Get("do").String()
	if err != nil {
		return err
	}
	switch do {
	case "copy":
		return p.Copy(ctx, op)
	case "exec":
		return p.Exec(ctx, op)
	case "export":
		return p.Export(ctx, op)
	case "fetch-container":
		return p.FetchContainer(ctx, op)
	case "fetch-git":
		return p.FetchGit(ctx, op)
	case "local":
		return p.Local(ctx, op)
	case "load":
		return p.Load(ctx, op)
	case "subdir":
		return p.Subdir(ctx, op)
	case "docker-build":
		return p.DockerBuild(ctx, op)
	default:
		return fmt.Errorf("invalid operation: %s", op.JSON())
	}
}

func (p *Pipeline) vertexNamef(format string, a ...interface{}) string {
	prefix := fmt.Sprintf("@%s@", p.name)
	name := fmt.Sprintf(format, a...)
	return prefix + " " + name
}

// Spawn a temporary pipeline with the same solver.
// Output values are discarded: the parent pipeline's values are not modified.
func (p *Pipeline) Tmp(name string) *Pipeline {
	return NewPipeline(name, p.s, nil)
}

func (p *Pipeline) Subdir(ctx context.Context, op *compiler.Value) error {
	// FIXME: this could be more optimized by carrying subdir path as metadata,
	//  and using it in copy, load or mount.

	dir, err := op.Get("dir").String()
	if err != nil {
		return err
	}
	p.fs = p.fs.Change(func(st llb.State) llb.State {
		return st.File(
			llb.Copy(
				p.fs.LLB(),
				dir,
				"/",
				&llb.CopyInfo{
					CopyDirContentsOnly: true,
				},
			),
			llb.WithCustomName(p.vertexNamef("Subdir %s", dir)),
		)
	})
	return nil
}

func (p *Pipeline) Copy(ctx context.Context, op *compiler.Value) error {
	// Decode copy options
	src, err := op.Get("src").String()
	if err != nil {
		return err
	}
	dest, err := op.Get("dest").String()
	if err != nil {
		return err
	}
	// Execute 'from' in a tmp pipeline, and use the resulting fs
	from := p.Tmp(op.Get("from").Path().String())
	if err := from.Do(ctx, op.Get("from")); err != nil {
		return err
	}
	p.fs = p.fs.Change(func(st llb.State) llb.State {
		return st.File(
			llb.Copy(
				from.FS().LLB(),
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
		)
	})
	return nil
}

func (p *Pipeline) Local(ctx context.Context, op *compiler.Value) error {
	dir, err := op.Get("dir").String()
	if err != nil {
		return err
	}
	var include []string
	if inc := op.Get("include"); inc.Exists() {
		if err := inc.Decode(&include); err != nil {
			return err
		}
	}

	p.fs = p.fs.Set(
		llb.Local(
			dir,
			llb.FollowPaths(include),
			llb.WithCustomName(p.vertexNamef("Local %s", dir)),
		),
	)
	return nil
}

func (p *Pipeline) Exec(ctx context.Context, op *compiler.Value) error {
	opts := []llb.RunOption{}
	var cmd struct {
		Args   []string
		Env    map[string]string
		Dir    string
		Always bool
	}

	if err := op.Decode(&cmd); err != nil {
		return err
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
			return err
		}
		opts = append(opts, llb.AddEnv("DAGGER_CACHEBUSTER", cacheBuster))
	}
	// mounts
	if mounts := op.Lookup("mount"); mounts.Exists() {
		mntOpts, err := p.mountAll(ctx, mounts)
		if err != nil {
			return err
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
	p.fs = p.fs.Change(func(st llb.State) llb.State {
		return st.Run(opts...).Root()
	})
	return nil
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
		case "tmp":
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
	from := p.Tmp(mnt.Get("from").Path().String())
	if err := from.Do(ctx, mnt.Get("from")); err != nil {
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
	return llb.AddMount(dest, from.FS().LLB(), mo...), nil
}

func (p *Pipeline) Export(ctx context.Context, op *compiler.Value) error {
	source, err := op.Get("source").String()
	if err != nil {
		return err
	}
	format, err := op.Get("format").String()
	if err != nil {
		return err
	}
	contents, err := p.fs.ReadFile(ctx, source)
	if err != nil {
		return fmt.Errorf("export %s: %w", source, err)
	}
	switch format {
	case "string":
		log.
			Ctx(ctx).
			Debug().
			Bytes("contents", contents).
			Msg("exporting string")

		if err := p.out.Fill(string(contents)); err != nil {
			return err
		}
	case "json":
		var o interface{}
		o, err := unmarshalAnything(contents, json.Unmarshal)
		if err != nil {
			return err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting json")

		if err := p.out.Fill(o); err != nil {
			return err
		}
	case "yaml":
		var o interface{}
		o, err := unmarshalAnything(contents, yaml.Unmarshal)
		if err != nil {
			return err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting yaml")

		if err := p.out.Fill(o); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported export format: %q", format)
	}
	return nil
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

func (p *Pipeline) Load(ctx context.Context, op *compiler.Value) error {
	// Execute 'from' in a tmp pipeline, and use the resulting fs
	from := p.Tmp(op.Get("from").Path().String())
	if err := from.Do(ctx, op.Get("from")); err != nil {
		return err
	}

	p.fs = p.fs.Set(from.FS().LLB())
	return nil
}

func (p *Pipeline) FetchContainer(ctx context.Context, op *compiler.Value) error {
	ref, err := op.Get("ref").String()
	if err != nil {
		return err
	}
	// FIXME: preserve docker image metadata
	p.fs = p.fs.Set(
		llb.Image(ref, llb.WithCustomName(p.vertexNamef("FetchContainer %s", ref))),
	)
	return nil
}

func (p *Pipeline) FetchGit(ctx context.Context, op *compiler.Value) error {
	remote, err := op.Get("remote").String()
	if err != nil {
		return err
	}
	ref, err := op.Get("ref").String()
	if err != nil {
		return err
	}
	p.fs = p.fs.Set(
		llb.Git(remote, ref, llb.WithCustomName(p.vertexNamef("FetchGit %s@%s", remote, ref))),
	)
	return nil
}

func (p *Pipeline) DockerBuild(ctx context.Context, op *compiler.Value) error {
	var (
		context    = op.Lookup("context")
		dockerfile = op.Lookup("dockerfile")

		contextDef    *bkpb.Definition
		dockerfileDef *bkpb.Definition

		err error
	)

	if !context.Exists() && !dockerfile.Exists() {
		return errors.New("context or dockerfile required")
	}

	// docker build context. This can come from another component, so we need to
	// compute it first.
	if context.Exists() {
		from := p.Tmp(op.Lookup("context").Path().String())
		if err := from.Do(ctx, context); err != nil {
			return err
		}
		contextDef, err = from.FS().Def(ctx)
		if err != nil {
			return err
		}
		dockerfileDef = contextDef
	}

	// Inlined dockerfile: need to be converted to LLB
	if dockerfile.Exists() {
		content, err := dockerfile.String()
		if err != nil {
			return err
		}
		dockerfileDef, err = p.s.Scratch().Set(
			llb.Scratch().File(
				llb.Mkfile("/Dockerfile", 0644, []byte(content)),
			),
		).Def(ctx)
		if err != nil {
			return err
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
			return err
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
			return err
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
			return err
		}
	}

	if platforms := op.Lookup("platforms"); platforms.Exists() {
		p := []string{}
		list, err := platforms.List()
		if err != nil {
			return err
		}

		for _, platform := range list {
			s, err := platform.String()
			if err != nil {
				return err
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
		return err
	}
	st, err := res.ToState()
	if err != nil {
		return err
	}
	p.fs = p.fs.Set(st)

	return nil
}
