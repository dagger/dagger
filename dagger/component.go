package dagger

import (
	"context"
	"os"

	"github.com/pkg/errors"
)

type Component struct {
	// Source value for the component, without spec merged
	// eg. `{ string, #dagger: compute: [{do:"fetch-container", ...}]}`
	v *Value

	// Annotation value for the component , with spec merged.
	// -> the contents of #dagger.compute
	// eg. `compute: [{do:"fetch-container", ...}]`
	//
	// The spec is merged at this level because the Cue API
	//  does not support merging embedded scalar with nested definition.
	config *Value
}

func NewComponent(v *Value) (*Component, error) {
	config := v.Get("#dagger")
	if !config.Exists() {
		return nil, os.ErrNotExist
	}
	spec := v.cc.Spec()
	config, err := spec.Get("#ComponentConfig").Merge(v.Get("#dagger"))
	if err != nil {
		return nil, errors.Wrap(err, "invalid component config")
	}
	return &Component{
		v:      v,
		config: config,
	}, nil
}

func (c *Component) Value() *Value {
	return c.v
}

// Return the contents of the "#dagger" annotation.
func (c *Component) Config() *Value {
	return c.Value().Get("#dagger")
}

// Verify that this component respects the dagger component spec.
//
// NOTE: calling matchSpec("#Component") is not enough because
//   it does not match embedded scalars.
func (c *Component) Validate() error {
	// FIXME: this crashes on `#dagger:compute:_`
	//  see TestValidateEmptyComponent
	// Using a workaround for now.
	// return c.Config().Validate("#ComponentConfig")

	return c.Config().Validate()
}

// Return this component's compute script.
func (c *Component) ComputeScript() (*Script, error) {
	return newScript(c.Config().Get("compute"))
}

// Compute the configuration for this component.
//
// Difference with Execute:
//
// 1. Always start with an empty fs state (Execute may receive any state as input)
// 2. Always solve at the end (Execute is lazy)
//
func (c *Component) Compute(ctx context.Context, s Solver, out *Fillable) (FS, error) {
	fs, err := c.Execute(ctx, s.Scratch(), out)
	if err != nil {
		return fs, err
	}

	// Force a `Solve()` in case it hasn't been called earlier.
	// If the FS is already solved, this is a noop.
	_, err = fs.Solve(ctx)
	return fs, err
}

// A component implements the Executable interface by returning its
// compute script.
// See Value.Executable().
func (c *Component) Execute(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	script, err := c.ComputeScript()
	if err != nil {
		// If the component has no script, then do not fail.
		if os.IsNotExist(err) {
			return fs, nil
		}
		return fs, err
	}
	return script.Execute(ctx, fs, out)
}

func (c *Component) Walk(ctx context.Context, fn func(*Op) error) error {
	script, err := c.ComputeScript()
	if err != nil {
		return err
	}
	return script.Walk(ctx, fn)
}
