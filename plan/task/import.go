package task

import (
	"context"
	"fmt"
	"os"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Import", func() Task { return &importTask{} })
}

type importTask struct {
}

func (c importTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var dir *plancontext.Directory

	if err := v.Decode(&dir); err != nil {
		return nil, err
	}

	// Check that directory exists
	if _, err := os.Stat(dir.Path); os.IsNotExist(err) {
		return nil, fmt.Errorf("%q dir doesn't exist", dir.Path)
	}

	id := pctx.Directories.Register(dir)
	return compiler.Compile("", fmt.Sprintf(
		`fs: #up: [{do: "local", id: %q}]`,
		id,
	))
}
