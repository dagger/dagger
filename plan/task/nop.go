package task

import (
	"context"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Nop", func() Task { return &nopTask{} })
}

type nopTask struct {
}

func (t *nopTask) Run(_ context.Context, _ *plancontext.Context, _ *solver.Solver, _ *compiler.Value) (*compiler.Value, error) {
	return compiler.NewValue(), nil
}
