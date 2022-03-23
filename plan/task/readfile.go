package task

import (
	"context"
	"fmt"
	"io/fs"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ReadFile", func() Task { return &readFileTask{} })
}

type readFileTask struct {
}

func (t *readFileTask) Run(_ context.Context, pctx *plancontext.Context, _ *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}
	inputFS := solver.NewBuildkitFS(input.Result())

	// FIXME: we should create an intermediate image containing only `path`.
	// That way, on cache misses, we'll only download the layer with the file contents rather than the entire FS.
	contents, err := fs.ReadFile(inputFS, path)
	if err != nil {
		return nil, fmt.Errorf("ReadFile %s: %w", path, err)
	}

	output := compiler.NewValue()
	if err := output.FillPath(cue.ParsePath("contents"), string(contents)); err != nil {
		return nil, err
	}

	return output, nil
}
