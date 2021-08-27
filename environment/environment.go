package environment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/rs/zerolog/log"
)

type Environment struct {
	state *state.State

	// Layer 1: plan configuration
	plan *compiler.Value

	// Layer 2: user inputs
	input *compiler.Value

	// plan + inputs
	src *compiler.Value

	// Layer 3: computed values
	computed *compiler.Value
}

func New(st *state.State) (*Environment, error) {
	var err error

	e := &Environment{
		state: st,
	}

	e.plan, err = st.CompilePlan(context.TODO())
	if err != nil {
		return nil, err
	}

	e.input, err = st.CompileInputs()
	if err != nil {
		return nil, err
	}

	e.computed = compiler.NewValue()

	e.src = compiler.NewValue()
	if err := e.src.FillPath(cue.MakePath(), e.plan); err != nil {
		return nil, err
	}
	if err := e.src.FillPath(cue.MakePath(), e.input); err != nil {
		return nil, err
	}

	return e, nil
}

func (e *Environment) Name() string {
	return e.state.Name
}

func (e *Environment) Computed() *compiler.Value {
	return e.computed
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

	return dirs
}

// Up missing values in environment configuration, and write them to state.
func (e *Environment) Up(ctx context.Context, s solver.Solver) error {
	tr := otel.Tracer("environment")
	ctx, span := tr.Start(ctx, "environment.Up")
	defer span.End()

	// Orchestrate execution with cueflow
	flow := cueflow.New(
		&cueflow.Config{},
		e.src.Cue(),
		newTaskFunc(newPipelineRunner(e.src, e.computed, s)),
	)
	if err := flow.Run(ctx); err != nil {
		return err
	}

	// FIXME: canceling the context makes flow return `nil`
	// Check explicitly if the context is canceled.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
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

func newPipelineRunner(src, computed *compiler.Value, s solver.Solver) cueflow.RunnerFunc {
	return cueflow.RunnerFunc(func(t *cueflow.Task) error {
		ctx := t.Context()
		lg := log.
			Ctx(ctx).
			With().
			Str("component", t.Path().String()).
			Logger()
		ctx = lg.WithContext(ctx)

		tr := otel.Tracer("environment")
		ctx, span := tr.Start(ctx, fmt.Sprintf("compute: %s", t.Path().String()))
		defer span.End()

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
		p := NewPipeline(src, v, s)
		err := p.Run(ctx)
		if err != nil {
			// Record the error
			span.AddEvent("command", trace.WithAttributes(
				attribute.String("error", err.Error()),
			))

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

func (e *Environment) ScanInputs(ctx context.Context, mergeUserInputs bool) ([]*compiler.Value, error) {
	src := e.plan

	if mergeUserInputs {
		src = e.src
	}

	return ScanInputs(ctx, src), nil
}

func (e *Environment) ScanOutputs(ctx context.Context) ([]*compiler.Value, error) {
	src := compiler.NewValue()
	if err := src.FillPath(cue.MakePath(), e.plan); err != nil {
		return nil, err
	}
	if err := src.FillPath(cue.MakePath(), e.input); err != nil {
		return nil, err
	}

	if e.state.Computed != "" {
		computed, err := compiler.DecodeJSON("", []byte(e.state.Computed))
		if err != nil {
			return nil, err
		}

		if err := src.FillPath(cue.MakePath(), computed); err != nil {
			return nil, err
		}
	}

	return ScanOutputs(ctx, src), nil
}
