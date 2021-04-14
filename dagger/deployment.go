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
	"dagger.io/go/pkg/cuetils"
	"dagger.io/go/stdlib"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/rs/zerolog/log"
)

type Deployment struct {
	state *DeploymentState

	// Layer 1: plan configuration
	plan *compiler.Value

	// Layer 2: user inputs
	input *compiler.Value

	// Layer 3: computed values
	computed *compiler.Value
}

func NewDeployment(st *DeploymentState) (*Deployment, error) {
	d := &Deployment{
		state: st,

		plan:     compiler.NewValue(),
		input:    compiler.NewValue(),
		computed: compiler.NewValue(),
	}

	// Prepare inputs
	for _, input := range st.Inputs {
		v, err := input.Value.Compile()
		if err != nil {
			return nil, err
		}
		if input.Key == "" {
			err = d.input.FillPath(cue.MakePath(), v)
		} else {
			err = d.input.FillPath(cue.ParsePath(input.Key), v)
		}
		if err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (d *Deployment) ID() string {
	return d.state.ID
}

func (d *Deployment) Name() string {
	return d.state.Name
}

func (d *Deployment) PlanSource() Input {
	return d.state.PlanSource
}

func (d *Deployment) Plan() *compiler.Value {
	return d.plan
}

func (d *Deployment) Input() *compiler.Value {
	return d.input
}

func (d *Deployment) Computed() *compiler.Value {
	return d.computed
}

// LoadPlan loads the plan
func (d *Deployment) LoadPlan(ctx context.Context, s Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "deployment.LoadPlan")
	defer span.Finish()

	planSource, err := d.state.PlanSource.Compile()
	if err != nil {
		return err
	}

	p := NewPipeline("[internal] source", s)
	// execute updater script
	if err := p.Do(ctx, planSource); err != nil {
		return err
	}

	// Build a Cue config by overlaying the source with the stdlib
	sources := map[string]fs.FS{
		stdlib.Path: stdlib.FS,
		"/":         p.FS(),
	}
	plan, err := compiler.Build(sources)
	if err != nil {
		return fmt.Errorf("plan config: %w", err)
	}
	d.plan = plan

	return nil
}

// Scan all scripts in the deployment for references to local directories (do:"local"),
// and return all referenced directory names.
// This is used by clients to grant access to local directories when they are referenced
// by user-specified scripts.
func (d *Deployment) LocalDirs() map[string]string {
	dirs := map[string]string{}
	localdirs := func(code ...*compiler.Value) {
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
			code...,
		)
	}
	// 1. Scan the deployment state
	// FIXME: use a common `flow` instance to avoid rescanning the tree.
	src, err := compiler.InstanceMerge(d.plan, d.input)
	if err != nil {
		panic(err)
	}
	flow := cueflow.New(
		&cueflow.Config{},
		src.CueInst(),
		newTaskFunc(src.CueInst(), noOpRunner),
	)
	for _, t := range flow.Tasks() {
		v := compiler.Wrap(t.Value(), src.CueInst())
		localdirs(v.Lookup("#up"))
	}

	// 2. Scan the plan
	plan, err := d.state.PlanSource.Compile()
	if err != nil {
		panic(err)
	}
	localdirs(plan)
	return dirs
}

// Up missing values in deployment configuration, and write them to state.
func (d *Deployment) Up(ctx context.Context, s Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "deployment.Up")
	defer span.Finish()

	// Reset the computed values
	d.computed = compiler.NewValue()

	// Cueflow cue instance
	src, err := compiler.InstanceMerge(d.plan, d.input)
	if err != nil {
		return err
	}

	// Orchestrate execution with cueflow
	flow := cueflow.New(
		&cueflow.Config{},
		src.CueInst(),
		newTaskFunc(src.CueInst(), newPipelineRunner(src.CueInst(), d.computed, s)),
	)
	if err := flow.Run(ctx); err != nil {
		return err
	}

	return nil
}

type DownOpts struct{}

func (d *Deployment) Down(ctx context.Context, _ *DownOpts) error {
	panic("NOT IMPLEMENTED")
}

type QueryOpts struct{}

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

func newPipelineRunner(inst *cue.Instance, computed *compiler.Value, s Solver) cueflow.RunnerFunc {
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
		p := NewPipeline(t.Path().String(), s)
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

func (D *Deployment) ScanInputs() error {
	err := cuetils.Hack(D.state.Cue())
	if err != nil {
		return err
	}
	return nil
}
