package dagger

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"dagger.io/go/dagger/compiler"
	"dagger.io/go/stdlib"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/rs/zerolog/log"
)

type Env struct {
	// Env boot script, eg. `[{do:"local",dir:"."}]`
	// FIXME: rename to 'update' (script to update the env config)
	// FIXME: embed update script in base as '#update' ?
	// FIXME: simplify Env by making it single layer? Each layer is one env.

	// How to update the base configuration
	updater *compiler.Value

	// Layer 1: base configuration
	base *compiler.Value

	// Layer 2: user inputs
	input *compiler.Value

	// Layer 3: computed values
	output *compiler.Value

	// All layers merged together: base + input + output
	state *compiler.Value
}

func (env *Env) Updater() *compiler.Value {
	return env.updater
}

// Set the updater script for this environment.
func (env *Env) SetUpdater(v *compiler.Value) error {
	if v == nil {
		var err error
		v, err = compiler.Compile("", "[]")
		if err != nil {
			return err
		}
	}
	env.updater = v
	return nil
}

func NewEnv() (*Env, error) {
	empty := compiler.EmptyStruct()
	env := &Env{
		base:   empty,
		input:  empty,
		output: empty,
	}
	if err := env.mergeState(); err != nil {
		return nil, err
	}
	if err := env.SetUpdater(nil); err != nil {
		return nil, err
	}
	return env, nil
}

func (env *Env) State() *compiler.Value {
	return env.state
}

func (env *Env) Input() *compiler.Value {
	return env.input
}

func (env *Env) SetInput(i *compiler.Value) error {
	if i == nil {
		i = compiler.EmptyStruct()
	}
	env.input = i
	return env.mergeState()
}

// Update the base configuration
func (env *Env) Update(ctx context.Context, s Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "Env.Update")
	defer span.Finish()

	p := NewPipeline("[internal] source", s, nil)
	// execute updater script
	if err := p.Do(ctx, env.updater); err != nil {
		return err
	}

	// Build a Cue config by overlaying the source with the stdlib
	sources := map[string]fs.FS{
		stdlib.Path: stdlib.FS,
		"/":         p.FS(),
	}
	base, err := compiler.Build(sources)
	if err != nil {
		return fmt.Errorf("base config: %w", err)
	}
	env.base = base
	// Commit
	return env.mergeState()
}

func (env *Env) Base() *compiler.Value {
	return env.base
}

func (env *Env) Output() *compiler.Value {
	return env.output
}

// Scan all scripts in the environment for references to local directories (do:"local"),
// and return all referenced directory names.
// This is used by clients to grant access to local directories when they are referenced
// by user-specified scripts.
func (env *Env) LocalDirs() map[string]string {
	dirs := map[string]string{}
	localdirs := func(code ...*compiler.Value) {
		Analyze(
			func(op *compiler.Value) error {
				do, err := op.Get("do").String()
				if err != nil {
					return err
				}
				// nolint:goconst
				// FIXME: merge Env into Route, or fix the linter error
				if do != "local" {
					return nil
				}
				dir, err := op.Get("dir").String()
				if err != nil {
					return err
				}
				dirs[dir] = dir
				return nil
			},
			code...,
		)
	}
	// 1. Scan the environment state
	// FIXME: use a common `flow` instance to avoid rescanning the tree.
	inst := env.state.CueInst()
	flow := cueflow.New(&cueflow.Config{}, inst, newTaskFunc(inst, noOpRunner))
	for _, t := range flow.Tasks() {
		v := compiler.Wrap(t.Value(), inst)
		localdirs(v.Get("#compute"))
	}
	// 2. Scan the environment updater
	localdirs(env.Updater())
	return dirs
}

// FIXME: this is just a 3-way merge. Add var args to compiler.Value.Merge.
func (env *Env) mergeState() error {
	// FIXME: make this cleaner in *compiler.Value by keeping intermediary instances
	// FIXME: state.CueInst() must return an instance with the same
	//  contents as state.v, for the purposes of cueflow.
	//  That is not currently how *compiler.Value works, so we prepare the cue
	//  instance manually.
	//   --> refactor the compiler.Value API to do this for us.
	var (
		state     = compiler.EmptyStruct()
		stateInst = state.CueInst()
		err       error
	)

	stateInst, err = stateInst.Fill(env.base.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}
	stateInst, err = stateInst.Fill(env.input.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}
	stateInst, err = stateInst.Fill(env.output.Cue())
	if err != nil {
		return fmt.Errorf("merge output with base & input: %w", err)
	}

	state = compiler.Wrap(stateInst.Value(), stateInst)

	// commit
	env.state = state
	return nil
}

// Compute missing values in env configuration, and write them to state.
func (env *Env) Compute(ctx context.Context, s Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "Env.Compute")
	defer span.Finish()

	lg := log.Ctx(ctx)

	// Cueflow cue instance
	inst := env.state.CueInst()

	// Reset the output
	env.output = compiler.EmptyStruct()

	// Cueflow config
	flowCfg := &cueflow.Config{
		UpdateFunc: func(c *cueflow.Controller, t *cueflow.Task) error {
			if t == nil {
				return nil
			}

			lg := lg.
				With().
				Str("component", t.Path().String()).
				Str("state", t.State().String()).
				Logger()

			if t.State() != cueflow.Terminated {
				return nil
			}
			// Merge task value into output
			var err error
			env.output, err = env.output.MergePath(t.Value(), t.Path())
			if err != nil {
				lg.
					Error().
					Err(err).
					Msg("failed to fill task result")
				return err
			}
			return nil
		},
	}
	// Orchestrate execution with cueflow
	flow := cueflow.New(flowCfg, inst, newTaskFunc(inst, newPipelineRunner(inst, s)))
	if err := flow.Run(ctx); err != nil {
		return err
	}

	{
		span, _ := opentracing.StartSpanFromContext(ctx, "Env.Compute: merge state")
		defer span.Finish()

		return env.mergeState()
	}
}

func newTaskFunc(inst *cue.Instance, runner cueflow.RunnerFunc) cueflow.TaskFunc {
	return func(flowVal cue.Value) (cueflow.Runner, error) {
		v := compiler.Wrap(flowVal, inst)
		if !isComponent(v) {
			// No compute script
			return nil, nil
		}
		return runner, nil
	}
}

func noOpRunner(t *cueflow.Task) error {
	return nil
}

func newPipelineRunner(inst *cue.Instance, s Solver) cueflow.RunnerFunc {
	return cueflow.RunnerFunc(func(t *cueflow.Task) error {
		ctx := t.Context()
		lg := log.
			Ctx(ctx).
			With().
			Str("component", t.Path().String()).
			Logger()
		ctx = lg.WithContext(ctx)
		span, ctx := opentracing.StartSpanFromContext(ctx,
			fmt.Sprintf("compute: %s", t.Path().String()),
		)
		defer span.Finish()

		start := time.Now()
		lg.
			Info().
			Msg("computing")
		for _, dep := range t.Dependencies() {
			lg.
				Debug().
				Str("dependency", dep.Path().String()).
				Msg("dependency detected")
		}
		v := compiler.Wrap(t.Value(), inst)
		p := NewPipeline(t.Path().String(), s, NewFillable(t))
		err := p.Do(ctx, v)
		if err != nil {
			span.LogFields(otlog.String("error", err.Error()))
			ext.Error.Set(span, true)

			// FIXME: this should use errdefs.IsCanceled(err)
			if strings.Contains(err.Error(), "context canceled") {
				lg.
					Error().
					Dur("duration", time.Since(start)).
					Msg("canceled")
				return err
			}
			lg.
				Error().
				Dur("duration", time.Since(start)).
				Err(err).
				Msg("failed")
			return err
		}
		lg.
			Info().
			Dur("duration", time.Since(start)).
			Msg("completed")
		return nil
	})
}
