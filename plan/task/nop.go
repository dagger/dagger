package task

import (
	"context"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Nop", func() Task { return &nopTask{} })
}

type nopTask struct {
}

func (t *nopTask) GetReference() bkgw.Reference {
	return nil
}

func (t *nopTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	return v, nil
}
