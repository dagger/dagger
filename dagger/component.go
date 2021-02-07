package dagger

import (
	"context"
	"os"

	"dagger.cloud/go/dagger/cc"
	"github.com/pkg/errors"
)

type Component struct {
	// Source value for the component, without spec merged
	// eg. `{ string, #dagger: compute: [{do:"fetch-container", ...}]}`
	v *cc.Value
}

func NewComponent(v *cc.Value) (*Component, error) {
	if !v.Exists() {
		// Component value does not exist
		return nil, ErrNotExist
	}
	if !v.Get("#dagger").Exists() {
		// Component value exists but has no `#dagger` definition
		return nil, ErrNotExist
	}
	// Validate & merge with spec
	final, err := v.Finalize(spec.Get("#Component"))
	if err != nil {
		return nil, errors.Wrap(err, "invalid component")
	}
	return &Component{
		v: final,
	}, nil
}

func (c *Component) Value() *cc.Value {
	return c.v
}

// Return the contents of the "#dagger" annotation.
func (c *Component) Config() *cc.Value {
	return c.Value().Get("#dagger")
}

// Return this component's compute script.
func (c *Component) ComputeScript() (*Script, error) {
	return newScript(c.Config().Get("compute"))
}

// Return a list of local dirs required to compute this component.
// (Scanned from the arg `dir` of operations `do: "local"` in the
// compute script.
func (c *Component) LocalDirs(ctx context.Context) (map[string]string, error) {
	s, err := c.ComputeScript()
	if err != nil {
		return nil, err
	}
	return s.LocalDirs(ctx)
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
// See cc.Value.Executable().
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
