package task

import (
	"context"

	"dagger.io/dagger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Nop", func() Task { return &nopTask{} })
}

type nopTask struct {
}

func (t *nopTask) Run(_ context.Context, _ *plancontext.Context, _ *solver.Solver, c *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": v.Lookup("input"),
	})
}
