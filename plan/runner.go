package plan

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plan/task"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/telemetry"
	"go.dagger.io/dagger/telemetry/event"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
)

type Runner struct {
	pctx     *plancontext.Context
	target   cue.Path
	s        *solver.Solver
	tasks    sync.Map
	dryRun   bool
	computed *compiler.Value
	mirror   *compiler.Value
	l        sync.Mutex
}

func NewRunner(pctx *plancontext.Context, target cue.Path, s *solver.Solver, dryRun bool) *Runner {
	return &Runner{
		pctx:     pctx,
		target:   target,
		s:        s,
		dryRun:   dryRun,
		computed: compiler.NewValue(),
		mirror:   compiler.NewValue(),
	}
}

func (r *Runner) Run(ctx context.Context, src *compiler.Value) (*compiler.Value, error) {
	if !src.LookupPath(r.target).Exists() {
		return nil, fmt.Errorf("%s not found", r.target.String())
	}

	if err := r.update(cue.MakePath(), src); err != nil {
		return nil, err
	}

	flow := cueflow.New(
		&cueflow.Config{
			FindHiddenTasks: true,
		},
		src.Cue(),
		r.taskFunc,
	)

	if err := flow.Run(ctx); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		if compSrc, err := r.computed.Source(); err == nil {
			log.Ctx(ctx).Debug().Str("computed", string(compSrc)).Msg("computed values")
		}

		final := compiler.NewValue()
		if err := final.FillPath(cue.MakePath(), src); err != nil {
			return nil, err
		}

		if err := final.FillPath(cue.MakePath(), r.computed); err != nil {
			return nil, err
		}

		return final, nil
	}
}

func (r *Runner) update(p cue.Path, v *compiler.Value) error {
	r.l.Lock()
	defer r.l.Unlock()

	if err := r.mirror.FillPath(p, v); err != nil {
		return err
	}
	r.initTasks()
	return nil
}

func (r *Runner) initTasks() {
	flow := cueflow.New(
		&cueflow.Config{
			FindHiddenTasks: true,
		},
		r.mirror.Cue(),
		noOpRunner,
	)

	// Allow tasks under the target
	for _, t := range flow.Tasks() {
		if cuePathHasPrefix(t.Path(), r.target) {
			r.addTask(t)
		}
	}

	// If a `client` task is targeting an allowed task, allow the output task as well
	for _, t := range flow.Tasks() {
		if t.Path().Selectors()[0] != ClientSelector {
			continue
		}
		for _, dep := range t.Dependencies() {
			if r.shouldRun(dep.Path()) {
				r.addTask(t)
			}
		}
	}
}

func (r *Runner) addTask(t *cueflow.Task) {
	// avoid circular dependencies
	if _, ok := r.tasks.Load(t.Path().String()); ok {
		return
	}

	r.tasks.Store(t.Path().String(), struct{}{})

	for _, dep := range t.Dependencies() {
		r.addTask(dep)
	}
}

func (r *Runner) shouldRun(p cue.Path) bool {
	_, ok := r.tasks.Load(p.String())
	return ok
}

func (r *Runner) taskFunc(flowVal cue.Value) (cueflow.Runner, error) {
	v := compiler.Wrap(flowVal)
	handler, err := task.Lookup(v)
	if err != nil {
		// Not a task
		if err == task.ErrNotTask {
			return nil, nil
		}
		return nil, err
	}

	if !r.shouldRun(v.Path()) {
		return nil, nil
	}

	// Wrapper around `task.Run` that handles logging, tracing, etc.
	return cueflow.RunnerFunc(func(t *cueflow.Task) error {
		ctx := t.Context()
		taskPath := t.Path().String()
		lg := log.Ctx(ctx).With().Str("task", taskPath).Logger()
		ctx = lg.WithContext(ctx)
		ctx, span := otel.Tracer("dagger").Start(ctx, taskPath)
		defer span.End()
		tm := telemetry.Ctx(ctx)

		lg.Info().Str("state", task.StateComputing.String()).Msg(task.StateComputing.String())
		tm.Push(ctx, event.ActionTransition{
			Name:  taskPath,
			State: event.ActionStateRunning,
		})

		// Debug: dump dependencies
		for _, dep := range t.Dependencies() {
			lg.Debug().Str("dependency", dep.Path().String()).Msg("dependency detected")
		}

		if r.dryRun {
			lg.Info().Str("state", task.StateSkipped.String()).Msg(task.StateSkipped.String())
			return nil
		}

		start := time.Now()
		result, err := handler.Run(ctx, r.pctx, r.s, compiler.Wrap(t.Value()))
		if err != nil {
			// FIXME: this should use errdefs.IsCanceled(err)
			if strings.Contains(err.Error(), "context canceled") {
				lg.Error().Dur("duration", time.Since(start)).Str("state", task.StateCanceled.String()).Msg(task.StateCanceled.String())
				tm.Push(ctx, event.ActionTransition{
					Name:  taskPath,
					State: event.ActionStateCancelled,
				})
			} else {
				lg.Error().Dur("duration", time.Since(start)).Err(compiler.Err(err)).Str("state", task.StateFailed.String()).Msg(task.StateFailed.String())
				tm.Push(ctx, event.ActionTransition{
					Name:  taskPath,
					State: event.ActionStateFailed,
					Error: compiler.Err(err).Error(),
				})
			}
			return fmt.Errorf("%s: %w", t.Path().String(), compiler.Err(err))
		}

		lg.Info().Dur("duration", time.Since(start)).Str("state", task.StateCompleted.String()).Msg(task.StateCompleted.String())
		tm.Push(ctx, event.ActionTransition{
			Name:  taskPath,
			State: event.ActionStateCompleted,
		})

		// If the result is not concrete (e.g. empty value), there's nothing to merge.
		if !result.IsConcrete() {
			return nil
		}

		if src, err := result.Source(); err == nil {
			lg.Debug().Str("result", string(src)).Msg("merging task result")
		}

		// Mirror task result and re-scan tasks that should run.
		// FIXME: This yields some structural cycle errors.
		// if err := r.update(t.Path(), result); err != nil {
		// 	return err
		// }

		if err := t.Fill(result.Cue()); err != nil {
			lg.Error().Err(err).Msg("failed to fill task")
			return err
		}

		// Merge task value into computed
		if err := r.computed.FillPath(t.Path(), result); err != nil {
			lg.Error().Err(err).Msg("failed to fill computed")
			return err
		}

		return nil
	}), nil
}

func cuePathHasPrefix(p cue.Path, prefix cue.Path) bool {
	pathSelectors := p.Selectors()
	prefixSelectors := prefix.Selectors()

	if len(pathSelectors) < len(prefixSelectors) {
		return false
	}

	for i, sel := range prefixSelectors {
		if pathSelectors[i] != sel {
			return false
		}
	}

	return true
}

func noOpRunner(flowVal cue.Value) (cueflow.Runner, error) {
	v := compiler.Wrap(flowVal)
	_, err := task.Lookup(v)
	if err != nil {
		// Not a task
		if err == task.ErrNotTask {
			return nil, nil
		}
		return nil, err
	}

	// Return a no op runner
	return cueflow.RunnerFunc(func(t *cueflow.Task) error {
		return nil
	}), nil
}
