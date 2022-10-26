package task

import (
	"context"
	"fmt"

	"cuelang.org/go/cue"
	"dagger.io/dagger"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ReadFile", func() Task { return &readFileTask{} })
}

type readFileTask struct {
}

func (t *readFileTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	fsid, err := utils.GetFSId(v.Lookup("input"))

	if err != nil {
		return nil, err
	}

	dgr := s.Client

	file, err := dgr.Directory(dagger.DirectoryOpts{ID: dagger.DirectoryID(fsid)}).File(path).Contents(ctx)

	if err != nil {
		return nil, fmt.Errorf("ReadFile %s: %w", path, err)
	}

	output := compiler.NewValue()
	if err := output.FillPath(cue.ParsePath("contents"), file); err != nil {
		return nil, err
	}

	return output, nil
}
