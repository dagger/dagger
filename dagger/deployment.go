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

// Contents of a deployment serialized to a file
type DeploymentState struct {
	// Globally unique deployment ID
	ID string `json:"id,omitempty"`

	// Human-friendly deployment name.
	// A deployment may have more than one name.
	// FIXME: store multiple names?
	Name string `json:"name,omitempty"`

	// Cue module containing the deployment layout
	// The input's top-level artifact is used as a module directory.
	LayoutSource Input `json:"layout,omitempty"`

	Inputs []inputKV `json:"inputs,omitempty"`
}

type inputKV struct {
	Key   string `json:"key,omitempty"`
	Value Input  `json:"value,omitempty"`
}

func (s *DeploymentState) AddInput(key string, value Input) error {
	s.Inputs = append(s.Inputs, inputKV{Key: key, Value: value})
	return nil
}

// Remove all inputs at the given key, including sub-keys.
// For example RemoveInputs("foo.bar") will remove all inputs
//   at foo.bar, foo.bar.baz, etc.
func (s *DeploymentState) RemoveInputs(key string) error {
	newInputs := make([]inputKV, 0, len(s.Inputs))
	for _, i := range s.Inputs {
		if i.Key == key {
			continue
		}
		newInputs = append(newInputs, i)
	}
	s.Inputs = newInputs

	return nil
}

type Deployment struct {
	st *DeploymentState

	// Layer 1: layout configuration
	layout *compiler.Value

	// Layer 2: user inputs
	input *compiler.Value

	// Layer 3: computed values
	output *compiler.Value

	// All layers merged together: layout + input + output
	state *compiler.Value
}

func NewDeployment(st *DeploymentState) (*Deployment, error) {
	empty := compiler.EmptyStruct()
	d := &Deployment{
		st:     st,
		layout: empty,
		input:  empty,
		output: empty,
	}

	// Prepare inputs
	for _, input := range st.Inputs {
		v, err := input.Value.Compile()
		if err != nil {
			return nil, err
		}
		if input.Key == "" {
			d.input, err = d.input.Merge(v)
		} else {
			d.input, err = d.input.MergeTarget(v, input.Key)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := d.mergeState(); err != nil {
		return nil, err
	}

	return d, nil
}

func (d *Deployment) ID() string {
	return d.st.ID
}

func (d *Deployment) Name() string {
	return d.st.Name
}

func (d *Deployment) LayoutSource() Input {
	return d.st.LayoutSource
}

func (d *Deployment) Layout() *compiler.Value {
	return d.layout
}

func (d *Deployment) Input() *compiler.Value {
	return d.input
}

func (d *Deployment) Output() *compiler.Value {
	return d.output
}

func (d *Deployment) State() *compiler.Value {
	return d.state
}

// LoadLayout loads the layout
func (d *Deployment) LoadLayout(ctx context.Context, s Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "deployment.LoadLayout")
	defer span.Finish()

	layoutSource, err := d.st.LayoutSource.Compile()
	if err != nil {
		return err
	}

	p := NewPipeline("[internal] source", s, nil)
	// execute updater script
	if err := p.Do(ctx, layoutSource); err != nil {
		return err
	}

	// Build a Cue config by overlaying the source with the stdlib
	sources := map[string]fs.FS{
		stdlib.Path: stdlib.FS,
		"/":         p.FS(),
	}
	layout, err := compiler.Build(sources)
	if err != nil {
		return fmt.Errorf("layout config: %w", err)
	}
	d.layout = layout

	// Commit
	return d.mergeState()
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
				do, err := op.Get("do").String()
				if err != nil {
					return err
				}
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
	// 1. Scan the deployment state
	// FIXME: use a common `flow` instance to avoid rescanning the tree.
	inst := d.state.CueInst()
	flow := cueflow.New(&cueflow.Config{}, inst, newTaskFunc(inst, noOpRunner))
	for _, t := range flow.Tasks() {
		v := compiler.Wrap(t.Value(), inst)
		localdirs(v.Get("#compute"))
	}

	// 2. Scan the layout
	layout, err := d.st.LayoutSource.Compile()
	if err != nil {
		panic(err)
	}
	localdirs(layout)
	return dirs
}

// FIXME: this is just a 3-way merge. Add var args to compiler.Value.Merge.
func (d *Deployment) mergeState() error {
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

	stateInst, err = stateInst.Fill(d.layout.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}
	stateInst, err = stateInst.Fill(d.input.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}
	stateInst, err = stateInst.Fill(d.output.Cue())
	if err != nil {
		return fmt.Errorf("merge output with base & input: %w", err)
	}

	state = compiler.Wrap(stateInst.Value(), stateInst)

	// commit
	d.state = state
	return nil
}

type UpOpts struct{}

// Up missing values in deployment configuration, and write them to state.
func (d *Deployment) Up(ctx context.Context, s Solver, _ *UpOpts) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "r.Compute")
	defer span.Finish()

	lg := log.Ctx(ctx)

	// Cueflow cue instance
	inst := d.state.CueInst()

	// Reset the output
	d.output = compiler.EmptyStruct()

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
			d.output, err = d.output.MergePath(t.Value(), t.Path())
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
		span, _ := opentracing.StartSpanFromContext(ctx, "merge state")
		defer span.Finish()

		return d.mergeState()
	}
}

type DownOpts struct{}

func (d *Deployment) Down(ctx context.Context, _ *DownOpts) error {
	panic("NOT IMPLEMENTED")
}

func (d *Deployment) Query(ctx context.Context, expr interface{}, o *QueryOpts) (*compiler.Value, error) {
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
