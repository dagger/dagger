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
	// Env boot script, eg. `[{do:"local",dir:"."}]`
	// FIXME: rename to 'update' (script to update the env config)
	// FIXME: embed update script in base as '#update' ?
	// FIXME: simplify Env by making it single layer? Each layer is one env.

	// Script to update the base configuration
	updater *Script

	// Layer 1: base configuration
	base *Value

	// Layer 2: user inputs
	input *Value

	// Layer 3: computed values
	output *Value

	// All layers merged together: base + input + output
	state *Value

	// Use the same cue compiler for everything
	cc *Compiler
}

func (env *Env) Updater() *Script {
	return env.updater
}

// Set the updater script for this environment.
// u may be:
//  - A compiled script: *Script
//  - A compiled value: *Value
//  - A cue source: string, []byte, io.Reader
func (env *Env) SetUpdater(u interface{}) error {
	if v, ok := u.(*Value); ok {
		updater, err := NewScript(v)
		if err != nil {
			return errors.Wrap(err, "invalid updater script")
		}
		env.updater = updater
		return nil
	}
	if updater, ok := u.(*Script); ok {
		env.updater = updater
		return nil
	}
	if u == nil {
		u = "[]"
	}
	updater, err := env.cc.CompileScript("updater", u)
	if err != nil {
		return err
	}
	env.updater = updater
	return nil
}

func NewEnv() (*Env, error) {
	cc := &Compiler{}
	empty, err := cc.EmptyStruct()
	if err != nil {
		return nil, err
	}
	env := &Env{
		cc:     cc,
		base:   empty,
		input:  empty,
		output: empty,
		state:  empty,
	}
	if err := env.SetUpdater(nil); err != nil {
		return nil, err
	}
	return env, nil
}

func (env *Env) Compiler() *Compiler {
	return env.cc
}

func (env *Env) State() *Value {
	return env.state
}

func (env *Env) Input() *Value {
	return env.input
}

func (env *Env) SetInput(i interface{}) error {
	if input, ok := i.(*Value); ok {
		return env.set(
			env.base,
			input,
			env.output,
		)
	}
	if i == nil {
		i = "{}"
	}
	input, err := env.cc.Compile("input", i)
	if err != nil {
		return err
	}
	return env.set(
		env.base,
		input,
		env.output,
	)
}

// Update the base configuration
func (env *Env) Update(ctx context.Context, s Solver) error {
	// execute updater script
	src, err := env.updater.Execute(ctx, s.Scratch(), nil)
	if err != nil {
		return err
	}
	// load cue files produced by updater
	// FIXME: BuildAll() to force all files (no required package..)
	base, err := env.cc.Build(ctx, src)
	if err != nil {
		return errors.Wrap(err, "base config")
	}
	return env.set(
		base,
		env.input,
		env.output,
	)
}

func (env *Env) Base() *Value {
	return env.base
}

func (env *Env) Output() *Value {
	return env.output
}

// Scan all scripts in the environment for references to local directories (do:"local"),
// and return all referenced directory names.
// This is used by clients to grant access to local directories when they are referenced
// by user-specified scripts.
func (env *Env) LocalDirs(ctx context.Context) (map[string]string, error) {
	lg := log.Ctx(ctx)
	dirs := map[string]string{}
	lg.Debug().
		Str("func", "Env.LocalDirs").
		Str("state", env.state.SourceUnsafe()).
		Str("updater", env.updater.Value().SourceUnsafe()).
		Msg("starting")
	defer func() {
		lg.Debug().Str("func", "Env.LocalDirs").Interface("result", dirs).Msg("done")
	}()
	// 1. Walk env state, scan compute script for each component.
	_, err := env.Walk(ctx, func(ctx context.Context, c *Component, out *Fillable) error {
		lg.Debug().
			Str("func", "Env.LocalDirs").
			Str("component", c.Value().Path().String()).
			Msg("scanning next component for local dirs")
		cdirs, err := c.LocalDirs(ctx)
		if err != nil {
			return err
		}
		for k, v := range cdirs {
			dirs[k] = v
		}
		return nil
	})
	if err != nil {
		return dirs, err
	}
	// 2. Scan updater script
	updirs, err := env.updater.LocalDirs(ctx)
	if err != nil {
		return dirs, err
	}
	for k, v := range updirs {
		dirs[k] = v
	}
	return dirs, nil
}

// Compute missing values in env configuration, and write them to state.
func (env *Env) Compute(ctx context.Context, s Solver) error {
	output, err := env.Walk(ctx, func(ctx context.Context, c *Component, out *Fillable) error {
		lg := log.Ctx(ctx)

		lg.
			Debug().
			Msg("[Env.Compute] processing")
		if _, err := c.Compute(ctx, s, out); err != nil {
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
	return env.set(
		env.base,
		env.input,
		output,
	)
}

// FIXME: this is just a 3-way merge. Add var args to Value.Merge.
func (env *Env) set(base, input, output *Value) (err error) {
	// FIXME: make this cleaner in *Value by keeping intermediary instances
	// FIXME: state.CueInst() must return an instance with the same
	//  contents as state.v, for the purposes of cueflow.
	//  That is not currently how *Value works, so we prepare the cue
	//  instance manually.
	//   --> refactor the Value API to do this for us.
	stateInst := env.state.CueInst()

	stateInst, err = stateInst.Fill(base.val)
	if err != nil {
		return errors.Wrap(err, "merge base & input")
	}
	stateInst, err = stateInst.Fill(input.val)
	if err != nil {
		return errors.Wrap(err, "merge base & input")
	}
	stateInst, err = stateInst.Fill(output.val)
	if err != nil {
		return errors.Wrap(err, "merge output with base & input")
	}

	state := env.cc.Wrap(stateInst.Value(), stateInst)

	// commit
	env.base = base
	env.input = input
	env.output = output
	env.state = state
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

	// FIXME: Exporting base/input/output separately causes merge errors.
	// For instance, `foo: string | *"default foo"` gets serialized as
	// `{"foo":"default foo"}`, which will fail to merge if output contains
	// a different definition of `foo`.
	//
	// fs = env.base.SaveJSON(fs, "base.cue")
	// fs = env.input.SaveJSON(fs, "input.cue")
	// if env.output != nil {
	// 	fs = env.output.SaveJSON(fs, "output.cue")
	// }
	// For now, export a single `state.cue` containing the combined output.
	var err error
	state := env.state
	if env.output != nil {
		state, err = state.Merge(env.output)
		if err != nil {
			return fs, err
		}
	}
	fs = state.SaveJSON(fs, "state.cue")
	return fs, nil
}

// FIXME: don't need ctx here
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
