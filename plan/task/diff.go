package task

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Diff", func() Task { return &diffTask{} })
}

type diffTask struct {
	ref bkgw.Reference
}

func (t *diffTask) GetReference() bkgw.Reference {
	return t.ref
}

func (t *diffTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lowerFS, err := pctx.FS.FromValue(v.Lookup("lower"))
	if err != nil {
		return nil, err
	}
	lower, err := lowerFS.State()
	if err != nil {
		return nil, err
	}

	upperFS, err := pctx.FS.FromValue(v.Lookup("upper"))
	if err != nil {
		return nil, err
	}
	upper, err := upperFS.State()
	if err != nil {
		return nil, err
	}

	st := llb.Diff(lower, upper)
	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}
