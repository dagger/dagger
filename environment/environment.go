package environment

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"
	"go.dagger.io/dagger/stdlib"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/rs/zerolog/log"
)

type Environment struct {
	state *state.State

	// Layer 1: plan configuration
	plan *compiler.Value

	// Layer 2: user inputs
	input *compiler.Value

	// Layer 3: computed values
	computed *compiler.Value
}

func New(st *state.State) (*Environment, error) {
	e := &Environment{
		state: st,

		plan:     compiler.NewValue(),
		input:    compiler.NewValue(),
		computed: compiler.NewValue(),
	}

	// Prepare inputs
	for key, input := range st.Inputs {
		v, err := input.Compile(key, st)
		if err != nil {
			return nil, err
		}
		if key == "" {
			err = e.input.FillPath(cue.MakePath(), v)
		} else {
			err = e.input.FillPath(cue.ParsePath(key), v)
		}
		if err != nil {
			return nil, err
		}
	}

	return e, nil
}

func (e *Environment) Name() string {
	return e.state.Name
}

func (e *Environment) PlanSource() state.Input {
	return e.state.PlanSource()
}

func (e *Environment) Plan() *compiler.Value {
	return e.plan
}

func (e *Environment) Input() *compiler.Value {
	return e.input
}

func (e *Environment) Computed() *compiler.Value {
	return e.computed
}

// LoadPlan loads the plan
func (e *Environment) LoadPlan(ctx context.Context, s solver.Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "environment.LoadPlan")
	defer span.Finish()

	planSource, err := e.state.PlanSource().Compile("", e.state)
	if err != nil {
		return err
	}

	p := NewPipeline(planSource, s).WithCustomName("[internal] source")
	// execute updater script
	if err := p.Run(ctx); err != nil {
		return err
	}

	// Build a Cue config by overlaying the source with the stdlib
	sources := map[string]fs.FS{
		stdlib.Path: stdlib.FS,
		"/":         p.FS(),
	}
	plan, err := compiler.Build(sources)
	if err != nil {
		return fmt.Errorf("plan config: %w", compiler.Err(err))
	}
	e.plan = plan

	return nil
}

// Scan all scripts in the environment for references to local directories (do:"local"),
// and return all referenced directory names.
// This is used by clients to grant access to local directories when they are referenced
// by user-specified scripts.
func (e *Environment) LocalDirs() map[string]string {
	dirs := map[string]string{}
	localdirs := func(code *compiler.Value) {
		Analyze(
			func(op *compiler.Value) error {
				do, err := op.Lookup("do").String()
				if err != nil {
					return err
				}
				if do != "local" {
					return nil
				}
				dir, err := op.Lookup("dir").String()
				if err != nil {
					return err
				}
				dirs[dir] = dir
				return nil
			},
			code,
		)
	}
	// 1. Scan the environment state
	// FIXME: use a common `flow` instance to avoid rescanning the tree.
	src := compiler.NewValue()
	if err := src.FillPath(cue.MakePath(), e.plan); err != nil {
		return nil
	}
	if err := src.FillPath(cue.MakePath(), e.input); err != nil {
		return nil
	}
	flow := cueflow.New(
		&cueflow.Config{},
		src.Cue(),
		newTaskFunc(noOpRunner),
	)
	for _, t := range flow.Tasks() {
		v := compiler.Wrap(t.Value())
		localdirs(v.Lookup("#up"))
	}

	// 2. Scan the plan
	plan, err := e.state.PlanSource().Compile("", e.state)
	if err != nil {
		panic(err)
	}
	localdirs(plan)
	return dirs
}

// Up missing values in environment configuration, and write them to state.
func (e *Environment) Up(ctx context.Context, s solver.Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "environment.Up")
	defer span.Finish()

	// Reset the computed values
	e.computed = compiler.NewValue()

	src := compiler.NewValue()
	if err := src.FillPath(cue.MakePath(), e.plan); err != nil {
		return err
	}
	if err := src.FillPath(cue.MakePath(), e.input); err != nil {
		return err
	}

	// Orchestrate execution with cueflow
	flow := cueflow.New(
		&cueflow.Config{},
		src.Cue(),
		newTaskFunc(newPipelineRunner(e.computed, s)),
	)
	if err := flow.Run(ctx); err != nil {
		return err
	}

	return nil
}

type DownOpts struct{}

func (e *Environment) Down(ctx context.Context, _ *DownOpts) error {
	panic("NOT IMPLEMENTED")
}

type QueryOpts struct{}

func newTaskFunc(runner cueflow.RunnerFunc) cueflow.TaskFunc {
	return func(flowVal cue.Value) (cueflow.Runner, error) {
		v := compiler.Wrap(flowVal)
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

func newPipelineRunner(computed *compiler.Value, s solver.Solver) cueflow.RunnerFunc {
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
		v := compiler.Wrap(t.Value())
		p := NewPipeline(v, s)
		err := p.Run(ctx)
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

		// Mirror the computed values in both `Task` and `Result`
		if p.Computed().IsEmptyStruct() {
			return nil
		}

		if err := t.Fill(p.Computed().Cue()); err != nil {
			lg.
				Error().
				Err(err).
				Msg("failed to fill task")
			return err
		}

		// Merge task value into output
		if err := computed.FillPath(t.Path(), p.Computed()); err != nil {
			lg.
				Error().
				Err(err).
				Msg("failed to fill task result")
			return err
		}

		lg.
			Info().
			Dur("duration", time.Since(start)).
			Msg("completed")
		return nil
	})
}

func (e *Environment) ScanInputs(ctx context.Context) []*compiler.Value {
	return scanInputs(ctx, e.plan)
}
