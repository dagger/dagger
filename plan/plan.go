package plan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/pkg"
	"go.dagger.io/dagger/plan/task"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"go.opentelemetry.io/otel"
)

type Plan struct {
	config Config

	context *plancontext.Context
	source  *compiler.Value
}

type Config struct {
	Args []string
	With []string
}

func Load(ctx context.Context, cfg Config) (*Plan, error) {
	log.Ctx(ctx).Debug().Interface("args", cfg.Args).Msg("loading plan")

	// FIXME: vendoring path
	if err := pkg.Vendor(ctx, ""); err != nil {
		return nil, err
	}

	v, err := compiler.Build("", nil, cfg.Args...)
	if err != nil {
		return nil, err
	}

	for i, param := range cfg.With {
		log.Ctx(ctx).Debug().Interface("with", param).Msg("compiling overlay")
		paramV, err := compiler.Compile(fmt.Sprintf("with%v", i), param)
		if err != nil {
			return nil, err
		}

		log.Ctx(ctx).Debug().Interface("with", param).Msg("filling overlay")
		fillErr := v.FillPath(cue.MakePath(), paramV)
		if fillErr != nil {
			return nil, fillErr
		}
	}

	p := &Plan{
		config:  cfg,
		context: plancontext.New(),
		source:  v,
	}

	if err := p.configPlatform(); err != nil {
		return nil, err
	}

	if err := p.prepare(ctx); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *Plan) Context() *plancontext.Context {
	return p.context
}

func (p *Plan) Source() *compiler.Value {
	return p.source
}

// configPlatform load the platform specified in the context
// Buildkit will then run every operation using that platform
// If platform is not define, context keep default platform
func (p *Plan) configPlatform() error {
	platformField := p.source.Lookup("platform")

	// Ignore if platform is not set in `#Plan`
	if !platformField.Exists() {
		return nil
	}

	// Convert platform to string
	platform, err := platformField.String()
	if err != nil {
		return err
	}

	// Set platform to context
	err = p.context.Platform.Set(platform)
	if err != nil {
		return err
	}
	return nil
}

// prepare executes the pre-run hooks of tasks
func (p *Plan) prepare(ctx context.Context) error {
	flow := cueflow.New(
		&cueflow.Config{},
		p.source.Cue(),
		func(flowVal cue.Value) (cueflow.Runner, error) {
			v := compiler.Wrap(flowVal)
			t, err := task.Lookup(v)
			if err != nil {
				// Not a task
				if err == task.ErrNotTask {
					return nil, nil
				}
				return nil, err
			}
			r, ok := t.(task.PreRunner)
			if !ok {
				return nil, nil
			}

			return cueflow.RunnerFunc(func(t *cueflow.Task) error {
				ctx := t.Context()
				lg := log.Ctx(ctx).With().Str("task", t.Path().String()).Logger()
				ctx = lg.WithContext(ctx)

				if err := r.PreRun(ctx, p.context, compiler.Wrap(t.Value())); err != nil {
					return fmt.Errorf("%s: %w", t.Path().String(), err)
				}
				return nil
			}), nil
		},
	)
	return flow.Run(ctx)
}

// Up executes the plan
func (p *Plan) Up(ctx context.Context, s solver.Solver) (*compiler.Value, error) {
	ctx, span := otel.Tracer("dagger").Start(ctx, "plan.Up")
	defer span.End()

	computed := compiler.NewValue()

	flow := cueflow.New(
		&cueflow.Config{},
		p.source.Cue(),
		newRunner(p.context, s, computed),
	)
	if err := flow.Run(ctx); err != nil {
		return nil, err
	}

	if src, err := computed.Source(); err == nil {
		log.Ctx(ctx).Debug().Str("computed", string(src)).Msg("computed values")
	}

	// FIXME: canceling the context makes flow return `nil`
	// Check explicitly if the context is canceled.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return computed, nil
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
			lg := log.Ctx(ctx).With().Str("task", t.Path().String()).Logger()
			ctx = lg.WithContext(ctx)
			ctx, span := otel.Tracer("dagger").Start(ctx, fmt.Sprintf("up: %s", t.Path().String()))
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
				return fmt.Errorf("%s: %w", t.Path().String(), err)
			}

			lg.Info().Dur("duration", time.Since(start)).Str("state", string(environment.StateCompleted)).Msg(string(environment.StateCompleted))

			// If the result is not concrete (e.g. empty value), there's nothing to merge.
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
