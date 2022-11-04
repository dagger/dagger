package task

import (
	"context"

	"dagger.io/dagger"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Diff", func() Task { return &diffTask{} })
}

type diffTask struct {
}

func (t *diffTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	dgr := s.Client

	lowerFSID, err := utils.GetFSId(v.Lookup("lower"))

	if err != nil {
		return nil, err
	}

	upperFSID, err := utils.GetFSId(v.Lookup("upper"))

	if err != nil {
		return nil, err
	}

	diffID, err := dgr.Directory(dagger.DirectoryOpts{ID: dagger.DirectoryID(lowerFSID)}).Diff(dgr.Directory(dagger.DirectoryOpts{ID: dagger.DirectoryID(upperFSID)})).ID(ctx)
	if err != nil {
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewFS(dagger.DirectoryID(diffID)),
	})
}
