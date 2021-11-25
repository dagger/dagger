package plan

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/plan/task"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"
	"go.opentelemetry.io/otel"
)

type Plan struct {
	context *plancontext.Context
	source  *compiler.Value
}

func Load(ctx context.Context, path, pkg string) (*Plan, error) {
	// FIXME: universe vendoring
	if err := state.VendorUniverse(ctx, path); err != nil {
		return nil, err
	}

	v, err := compiler.Build(path, nil, pkg)
	if err != nil {
		return nil, err
	}

	return &Plan{
		context: plancontext.New(),
		source:  v,
	}, nil
}

func (p *Plan) Context() *plancontext.Context {
	return p.context
}

func (p *Plan) Source() *compiler.Value {
	return p.source
}

// LocalDirectories scans the context for local imports.
// BuildKit requires to known the list of directories ahead of time.
func (p *Plan) LocalDirectories() (map[string]string, error) {
	dirs := map[string]string{}

	imports, err := p.source.Lookup("context.imports").Fields()
	if err != nil {
		return nil, err
	}

	for _, v := range imports {
		dir, err := v.Value.Lookup("path").String()
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}

		dirs[dir] = abs
	}

	return dirs, nil
}

// Up executes the plan
func (p *Plan) Up(ctx context.Context, s solver.Solver) error {
	ctx, span := otel.Tracer("dagger").Start(ctx, "plan.Up")
	defer span.End()

	computed := compiler.NewValue()

	flow := cueflow.New(
		&cueflow.Config{},
		p.source.Cue(),
		newRunner(p.context, s, computed),
	)
	if err := flow.Run(ctx); err != nil {
		return err
	}

	if src, err := computed.Source(); err == nil {
		log.Ctx(ctx).Debug().Str("computed", string(src)).Msg("computed values")
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

func newRunner(pctx *plancontext.Context, s solver.Solver, computed *compiler.Value) cueflow.TaskFunc {
	return func(flowVal cue.Value) (cueflow.Runner, error) {
		v := compiler.Wrap(flowVal)
		r, err := task.Lookup(v)
		if err != nil {
			// Not a task
			if err == task.ErrNotTask {
				return nil, nil
			}
			return nil, err
		}

		// Wrapper around `task.Run` that handles logging, tracing, etc.
		return cueflow.RunnerFunc(func(t *cueflow.Task) error {
			ctx := t.Context()
			lg := log.Ctx(ctx).With().Str("component", t.Path().String()).Logger()
			ctx = lg.WithContext(ctx)
			ctx, span := otel.Tracer("dagger").Start(ctx, fmt.Sprintf("compute: %s", t.Path().String()))
			defer span.End()

			lg.Info().Str("state", string(environment.StateComputing)).Msg(string(environment.StateComputing))

			// Debug: dump dependencies
			for _, dep := range t.Dependencies() {
				lg.Debug().Str("dependency", dep.Path().String()).Msg("dependency detected")
			}

			start := time.Now()
			result, err := r.Run(ctx, pctx, s, compiler.Wrap(t.Value()))
			if err != nil {
				// FIXME: this should use errdefs.IsCanceled(err)
				if strings.Contains(err.Error(), "context canceled") {
					lg.Error().Dur("duration", time.Since(start)).Str("state", string(environment.StateCanceled)).Msg(string(environment.StateCanceled))
				} else {
					lg.Error().Dur("duration", time.Since(start)).Err(err).Str("state", string(environment.StateFailed)).Msg(string(environment.StateFailed))
				}
				return err
			}

			lg.Info().Dur("duration", time.Since(start)).Str("state", string(environment.StateCompleted)).Msg(string(environment.StateCompleted))

			// If the result is not concrete, there's nothing to merge.
			if !result.IsConcrete() {
				return nil
			}

			if src, err := result.Source(); err == nil {
				lg.Debug().Str("result", string(src)).Msg("merging task result")
			}

			// Mirror task result in both `flow.Task` and `computed`
			if err := t.Fill(result.Cue()); err != nil {
				lg.Error().Err(err).Msg("failed to fill task")
				return err
			}

			// Merge task value into computed
			if err := computed.FillPath(t.Path(), result); err != nil {
				lg.Error().Err(err).Msg("failed to fill plan")
				return err
			}

			return nil
		}), nil
	}
}
