package plan

import (
	"context"
	"fmt"

	"cuelang.org/go/cue"
	cueflow "cuelang.org/go/tools/flow"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/pkg"
	"go.dagger.io/dagger/plan/task"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"go.opentelemetry.io/otel"
)

var (
	ActionSelector = cue.Str("actions")
	ClientSelector = cue.Str("client")
)

type Plan struct {
	config Config

	context *plancontext.Context
	source  *compiler.Value
	action  *Action
}

type Config struct {
	Args   []string
	With   []string
	Target string
	Vendor bool
}

func Load(ctx context.Context, cfg Config) (*Plan, error) {
	log.Ctx(ctx).Debug().Interface("args", cfg.Args).Msg("loading plan")

	// FIXME: move vendoring to explicit project update command
	if cfg.Vendor {
		// FIXME: vendoring path
		if err := pkg.Vendor(ctx, ""); err != nil {
			return nil, err
		}
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

	p.fillAction()

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

func (p *Plan) Action() *Action {
	return p.action
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
		&cueflow.Config{
			FindHiddenTasks: true,
		},
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

// Do executes an action in the plan
func (p *Plan) Do(ctx context.Context, path cue.Path, s solver.Solver) error {
	ctx, span := otel.Tracer("dagger").Start(ctx, "plan.Up")
	defer span.End()

	r := NewRunner(p.context, path, s)
	return r.Run(ctx, p.source)
}

func (p *Plan) fillAction() {
	cfg := &cueflow.Config{
		FindHiddenTasks: true,
		Root:            cue.MakePath(ActionSelector),
	}

	flow := cueflow.New(
		cfg,
		p.source.Cue(),
		noOpRunner,
	)

	actions := p.source.LookupPath(cue.MakePath(ActionSelector))
	actionsComment := ""
	for _, cg := range actions.Doc() {
		actionsComment += cg.Text()
	}
	p.action = &Action{
		ActionSelector.String(),
		false,
		actions.Path(),
		actionsComment,
		[]*Action{},
	}

	tasks := flow.Tasks()

	for _, t := range tasks {
		var q []cue.Selector
		prevAction := p.action
		for _, s := range t.Path().Selectors() {
			q = append(q, s)
			path := cue.MakePath(q...)
			a := prevAction.FindByPath(path)
			if a == nil {
				v := p.Source().LookupPath(path)
				childComment := ""
				for _, cg := range v.Doc() {
					childComment += cg.Text()
				}

				a = &Action{
					s.String(),
					s.PkgPath() != "",
					path,
					childComment,
					[]*Action{},
				}
				prevAction.AddChild(a)
			}
			prevAction = a
		}
	}
}
