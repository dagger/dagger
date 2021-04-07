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

type Deployment struct {
	state  *DeploymentState
	result *DeploymentResult
}

func NewDeployment(st *DeploymentState) (*Deployment, error) {
	d := &Deployment{
		state:  st,
		result: NewDeploymentResult(),
	}

	// Prepare inputs
	for _, input := range st.Inputs {
		v, err := input.Value.Compile()
		if err != nil {
			return nil, err
		}
		if input.Key == "" {
			err = d.result.input.FillPath(cue.MakePath(), v)
		} else {
			err = d.result.input.FillPath(cue.ParsePath(input.Key), v)
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

func (d *Deployment) Result() *DeploymentResult {
	return d.result
}

// LoadPlan loads the plan
func (d *Deployment) LoadPlan(ctx context.Context, s Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "deployment.LoadPlan")
	defer span.Finish()

	planSource, err := d.state.PlanSource.Compile()
	if err != nil {
		return err
	}

	p := NewPipeline("[internal] source", s, nil)
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
	d.result.plan = plan

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
	src, err := d.result.Merge()
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

	lg := log.Ctx(ctx)

	// Reset the computed values
	d.result.computed = compiler.EmptyStruct()

	// Cueflow cue instance
	src, err := d.result.Merge()
	if err != nil {
		return err
	}

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
			err := d.result.computed.FillPath(t.Path(), t.Value())
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
	flow := cueflow.New(
		flowCfg,
		src.CueInst(),
		newTaskFunc(src.CueInst(), newPipelineRunner(src.CueInst(), s)),
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
