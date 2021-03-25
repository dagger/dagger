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

// Contents of a route serialized to a file
type RouteState struct {
	// Globally unique route ID
	ID string `json:"id,omitempty"`

	// Human-friendly route name.
	// A route may have more than one name.
	// FIXME: store multiple names?
	Name string `json:"name,omitempty"`

	// Cue module containing the route layout
	// The input's top-level artifact is used as a module directory.
	LayoutSource Input `json:"layout,omitempty"`

	Inputs []inputKV `json:"inputs,omitempty"`
}

type inputKV struct {
	Key   string `json:"key,omitempty"`
	Value Input  `json:"value,omitempty"`
}

func (r *RouteState) AddInput(key string, value Input) error {
	r.Inputs = append(r.Inputs, inputKV{Key: key, Value: value})
	return nil
}

// Remove all inputs at the given key, including sub-keys.
// For example RemoveInputs("foo.bar") will remove all inputs
//   at foo.bar, foo.bar.baz, etc.
func (r *RouteState) RemoveInputs(key string) error {
	panic("NOT IMPLEMENTED")
}

type Route struct {
	st *RouteState

	// Env boot script, eg. `[{do:"local",dir:"."}]`
	// FIXME: rename to 'update' (script to update the env config)
	// FIXME: embed update script in base as '#update' ?
	// FIXME: simplify Env by making it single layer? Each layer is one r.

	// Layer 1: layout configuration
	layout *compiler.Value

	// Layer 2: user inputs
	input *compiler.Value

	// Layer 3: computed values
	output *compiler.Value

	// All layers merged together: layout + input + output
	state *compiler.Value
}

func (r *Route) ID() string {
	return r.st.ID
}

func (r *Route) Name() string {
	return r.st.Name
}

func (r *Route) LayoutSource() Input {
	return r.st.LayoutSource
}

func NewRoute(st *RouteState) (*Route, error) {
	empty := compiler.EmptyStruct()
	r := &Route{
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
			r.input, err = r.input.Merge(v)
		} else {
			r.input, err = r.input.MergeTarget(v, input.Key)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := r.mergeState(); err != nil {
		return nil, err
	}

	return r, nil
}

func (r *Route) State() *compiler.Value {
	return r.state
}

// Update the base configuration
func (r *Route) Update(ctx context.Context, s Solver) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "r.Update")
	defer span.Finish()

	layout, err := r.st.LayoutSource.Compile()
	if err != nil {
		return err
	}

	p := NewPipeline("[internal] source", s, nil)
	// execute updater script
	if err := p.Do(ctx, layout); err != nil {
		return err
	}

	// Build a Cue config by overlaying the source with the stdlib
	sources := map[string]fs.FS{
		stdlib.Path: stdlib.FS,
		"/":         p.FS(),
	}
	base, err := compiler.Build(sources)
	if err != nil {
		return fmt.Errorf("base config: %w", err)
	}
	r.layout = base

	// Commit
	return r.mergeState()
}

func (r *Route) Base() *compiler.Value {
	return r.layout
}

func (r *Route) Output() *compiler.Value {
	return r.output
}

// Scan all scripts in the environment for references to local directories (do:"local"),
// and return all referenced directory names.
// This is used by clients to grant access to local directories when they are referenced
// by user-specified scripts.
func (r *Route) LocalDirs() map[string]string {
	dirs := map[string]string{}
	localdirs := func(code ...*compiler.Value) {
		Analyze(
			func(op *compiler.Value) error {
				do, err := op.Get("do").String()
				if err != nil {
					return err
				}
				// nolint:goconst
				// FIXME: merge Env into Route, or fix the linter error
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
	// 1. Scan the environment state
	// FIXME: use a common `flow` instance to avoid rescanning the tree.
	inst := r.state.CueInst()
	flow := cueflow.New(&cueflow.Config{}, inst, newTaskFunc(inst, noOpRunner))
	for _, t := range flow.Tasks() {
		v := compiler.Wrap(t.Value(), inst)
		localdirs(v.Get("#compute"))
	}

	// 2. Scan the layout
	if r.st.LayoutSource != nil {
		layout, err := r.st.LayoutSource.Compile()
		if err != nil {
			panic(err)
		}
		localdirs(layout)
	}
	return dirs
}

// FIXME: this is just a 3-way merge. Add var args to compiler.Value.Merge.
func (r *Route) mergeState() error {
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

	stateInst, err = stateInst.Fill(r.layout.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}
	stateInst, err = stateInst.Fill(r.input.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}
	stateInst, err = stateInst.Fill(r.output.Cue())
	if err != nil {
		return fmt.Errorf("merge output with base & input: %w", err)
	}

	state = compiler.Wrap(stateInst.Value(), stateInst)

	// commit
	r.state = state
	return nil
}

type UpOpts struct{}

// Up missing values in env configuration, and write them to state.
func (r *Route) Up(ctx context.Context, s Solver, _ *UpOpts) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "r.Compute")
	defer span.Finish()

	lg := log.Ctx(ctx)

	// Cueflow cue instance
	inst := r.state.CueInst()

	// Reset the output
	r.output = compiler.EmptyStruct()

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
			r.output, err = r.output.MergePath(t.Value(), t.Path())
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
		span, _ := opentracing.StartSpanFromContext(ctx, "r.Compute: merge state")
		defer span.Finish()

		return r.mergeState()
	}
}

type DownOpts struct{}

func (r *Route) Down(ctx context.Context, _ *DownOpts) error {
	panic("NOT IMPLEMENTED")
}

func (r *Route) Query(ctx context.Context, expr interface{}, o *QueryOpts) (*compiler.Value, error) {
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
