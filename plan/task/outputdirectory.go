package task

import (
	"context"

	bk "github.com/moby/buildkit/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("OutputDirectory", func() Task { return &outputDirectoryTask{} })
}

type outputDirectoryTask struct {
}

func (c outputDirectoryTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	contents, err := pctx.FS.FromValue(v.Lookup("contents"))
	if err != nil {
		return nil, err
	}

	dest, err := v.Lookup("dest").AbsPath()
	if err != nil {
		return nil, err
	}

	st, err := contents.Result().ToState()
	if err != nil {
		return nil, err
	}
	_, err = s.Export(ctx, st, nil, bk.ExportEntry{
		Type:      bk.ExporterLocal,
		OutputDir: dest,
	}, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	return compiler.NewValue(), nil
}
