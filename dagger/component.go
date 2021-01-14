package dagger

import (
	"context"
)

type Component struct {
	v *Value
}

func (c *Component) Value() *Value {
	return c.v
}

func (c *Component) Exists() bool {
	// Does #dagger exist?
	return c.Config().Exists()
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
	return c.Value().Get("#dagger.compute").Script()
}

// Compute the configuration for this component.
//
// Difference with Execute:
//
// 1. Always start with an empty fs state (Execute may receive any state as input)
// 2. Always solve at the end (Execute is lazy)
//
func (c *Component) Compute(ctx context.Context, s Solver, out Fillable) (FS, error) {
	fs, err := c.Execute(ctx, s.Scratch(), out)
	if err != nil {
		return fs, err
	}
	_, err = fs.ReadDir(ctx, "/")
	return fs, err
}

// A component implements the Executable interface by returning its
// compute script.
// See Value.Executable().
func (c *Component) Execute(ctx context.Context, fs FS, out Fillable) (FS, error) {
	script, err := c.ComputeScript()
	if err != nil {
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
