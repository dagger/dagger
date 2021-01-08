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
	if c.Config().Err() != nil {
		return false
	}
	return true
}

// Return the contents of the "#dagger" annotation.
func (c *Component) Config() *Value {
	return c.Value().Get("#dagger")
}

// Verify that this component respects the dagger component spec.
//
// NOTE: calling matchSpec("#Component") is not enough because
//   it does not match embedded scalars.
func (c Component) Validate() error {
	// FIXME: this crashes on `#dagger:compute:_`
	//  see TestValidateEmptyComponent
	// Using a workaround for now.
	// return c.Config().Validate("#ComponentConfig")

	return c.Config().Validate()
}

// Return this component's compute script.
func (c Component) ComputeScript() (*Script, error) {
	return c.Value().Get("#dagger.compute").Script()
}

// Compute the configuration for this component.
// Note that we simply execute the underlying compute script from an
// empty filesystem state.
// (It is never correct to pass an input filesystem state to compute a component)
func (c *Component) Compute(ctx context.Context, s Solver, out Fillable) (FS, error) {
	return c.Execute(ctx, s.Scratch(), out)
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

func (c *Component) Walk(fn func(*Op) error) error {
	script, err := c.ComputeScript()
	if err != nil {
		return err
	}
	return script.Walk(fn)
}
