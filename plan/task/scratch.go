package task

import (
	"context"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Scratch", func() Task { return &scratchTask{} })
}

type scratchTask struct {
}

func (t *scratchTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	fs := pctx.FS.New(nil)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}
