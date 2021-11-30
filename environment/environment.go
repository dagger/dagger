package environment

import (
	"context"
	"fmt"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
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

func (e *Environment) Context() *plancontext.Context {
	return e.state.Context
}

// Up missing values in environment configuration, and write them to state.
func (e *Environment) Up(ctx context.Context, s solver.Solver) error {
	ctx, span := otel.Tracer("dagger").Start(ctx, "environment.Up")
	defer span.End()

	// Orchestrate execution with cueflow
	flow := cueflow.New(
		&cueflow.Config{},
		e.src.Cue(),
		newTaskFunc(newPipelineRunner(e.computed, s, e.state.Context)),
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
		if !IsComponent(v) {
			// No compute script
			return nil, nil
		}
		return runner, nil
	}
}

func newPipelineRunner(computed *compiler.Value, s solver.Solver, pctx *plancontext.Context) cueflow.RunnerFunc {
	return cueflow.RunnerFunc(func(t *cueflow.Task) error {
		ctx := t.Context()
		lg := log.
			Ctx(ctx).
			With().
			Str("task", t.Path().String()).
			Logger()
		ctx = lg.WithContext(ctx)

		ctx, span := otel.Tracer("dagger").Start(ctx, fmt.Sprintf("compute: %s", t.Path().String()))
		defer span.End()

		for _, dep := range t.Dependencies() {
			lg.
				Debug().
				Str("dependency", dep.Path().String()).
				Msg("dependency detected")
		}
		v := compiler.Wrap(t.Value())

		p := NewPipeline(v, s, pctx)
		err := p.Run(ctx)
		if err != nil {
			// Record the error
			span.AddEvent("command", trace.WithAttributes(
				attribute.String("error", err.Error()),
			))

			return err
		}

		// Mirror the computed values in both `Task` and `Result`
		if !p.Computed().IsConcrete() {
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
