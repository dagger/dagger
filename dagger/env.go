package dagger

import (
	"context"
	"os"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

type Env struct {
	// Base config
	base *Value
	// Input overlay: user settings, external directories, secrets...
	input *Value

	// Output overlay: computed values, generated directories
	output *Value

	// Buildkit solver
	s Solver

	// Full cue state (base + input + output)
	state *Value

	// shared cue compiler
	// (because cue API requires shared runtime for everything)
	cc *Compiler
}

// Initialize a new environment
func NewEnv(ctx context.Context, s Solver, bootsrc, inputsrc string) (*Env, error) {
	lg := log.Ctx(ctx)

	lg.
		Debug().
		Str("boot", bootsrc).
		Str("input", inputsrc).
		Msg("New Env")

	cc := &Compiler{}
	// 1. Compile & execute boot script
	boot, err := cc.CompileScript("boot.cue", bootsrc)
	if err != nil {
		return nil, errors.Wrap(err, "compile boot script")
	}
	bootfs, err := boot.Execute(ctx, s.Scratch(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "execute boot script")
	}
	// 2. load cue files produced by boot script
	// FIXME: BuildAll() to force all files (no required package..)
	lg.Debug().Msg("building cue configuration from boot state")
	base, err := cc.Build(ctx, bootfs)
	if err != nil {
		return nil, errors.Wrap(err, "load base config")
	}
	// 3. Compile & merge input overlay (user settings, input directories, secrets.)
	lg.Debug().Msg("loading input overlay")
	input, err := cc.Compile("input.cue", inputsrc)
	if err != nil {
		return nil, err
	}
	// Merge base + input into a new cue instance
	// FIXME: make this cleaner in *Value by keeping intermediary instances
	stateInst, err := base.CueInst().Fill(input.CueInst().Value())
	if err != nil {
		return nil, errors.Wrap(err, "merge base & input")
	}
	state := cc.Wrap(stateInst.Value(), stateInst)

	lg.
		Debug().
		Str("base", base.JSON().String()).
		Str("input", input.JSON().String()).
		Msg("ENV")

	return &Env{
		base:  base,
		input: input,
		state: state,
		s:     s,
		cc:    cc,
	}, nil
}

// Compute missing values in env configuration, and write them to state.
func (env *Env) Compute(ctx context.Context) error {
	output, err := env.Walk(ctx, func(ctx context.Context, c *Component, out *Fillable) error {
		lg := log.Ctx(ctx)

		lg.
			Debug().
			Msg("[Env.Compute] processing")
		if _, err := c.Compute(ctx, env.s, out); err != nil {
			lg.
				Error().
				Err(err).
				Msg("component failed")
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	env.output = output
	return nil
}

// Export env to a directory of cue files
// (Use with FS.Change)
func (env *Env) Export(fs FS) (FS, error) {
	// FIXME: we serialize as JSON to guarantee a self-contained file.
	//   Value.Save() leaks imports, so requires a shared cue.mod with
	//   client which is undesirable.
	//   Once Value.Save() resolves non-builtin imports with a tree shake,
	//   we can use it here.
	fs = env.base.SaveJSON(fs, "base.cue")
	fs = env.input.SaveJSON(fs, "input.cue")
	if env.output != nil {
		fs = env.output.SaveJSON(fs, "output.cue")
	}
	return fs, nil
}

type EnvWalkFunc func(context.Context, *Component, *Fillable) error

// Walk components and return any computed values
func (env *Env) Walk(ctx context.Context, fn EnvWalkFunc) (*Value, error) {
	lg := log.Ctx(ctx)

	// Cueflow cue instance
	flowInst := env.state.CueInst()
	lg.
		Debug().
		Str("value", env.cc.Wrap(flowInst.Value(), flowInst).JSON().String()).
		Msg("walking")

	// Initialize empty output
	out, err := env.cc.EmptyStruct()
	if err != nil {
		return nil, err
	}

	// Cueflow config
	flowCfg := &cueflow.Config{
		UpdateFunc: func(c *cueflow.Controller, t *cueflow.Task) error {
			if t == nil {
				return nil
			}

			lg := lg.
				With().
				Str("path", t.Path().String()).
				Str("state", t.State().String()).
				Logger()

			lg.Debug().Msg("cueflow task")
			if t.State() != cueflow.Terminated {
				return nil
			}
			lg.Debug().Msg("cueflow task: filling result")
			// Merge task value into output
			var err error
			// FIXME: does cueflow.Task.Value() contain only filled values,
			// or base + filled?
			out, err = out.MergePath(t.Value(), t.Path())
			if err != nil {
				lg.
					Error().
					Err(err).
					Msg("failed to fill script result")
				return err
			}
			return nil
		},
	}
	// Cueflow match func
	flowMatchFn := func(v cue.Value) (cueflow.Runner, error) {
		lg := lg.
			With().
			Str("path", v.Path().String()).
			Logger()
		ctx := lg.WithContext(ctx)

		lg.Debug().Msg("Env.Walk: processing")
		// FIXME: get directly from state Value ? Locking issue?
		val := env.cc.Wrap(v, flowInst)
		c, err := NewComponent(val)
		if os.IsNotExist(err) {
			// Not a component: skip
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return cueflow.RunnerFunc(func(t *cueflow.Task) error {
			return fn(ctx, c, NewFillable(t))
		}), nil
	}
	// Orchestrate execution with cueflow
	flow := cueflow.New(flowCfg, flowInst, flowMatchFn)
	if err := flow.Run(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// Return the component at the specified path in the config, eg. `www`
// If the component does not exist, os.ErrNotExist is returned.
func (env *Env) Component(target string) (*Component, error) {
	return NewComponent(env.state.Get(target))
}
