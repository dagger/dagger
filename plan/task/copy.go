package task

import (
	"context"
	"fmt"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Copy", func() Task { return &copyTask{} })
}

type copyTask struct {
}

func (t *copyTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var err error

	input, err := pctx.FS.FromValue(v.Lookup("input"))

	if err != nil {
		return nil, err
	}

	inputState, err := input.Result().ToState()

	if err != nil {
		return nil, err
	}

	sourceRoot, err := pctx.FS.FromValue(v.Lookup("source.root"))

	if err != nil {
		return nil, err
	}

	sourceState, err := sourceRoot.Result().ToState()

	if err != nil {
		return nil, err
	}

	sourcePath, err := v.Lookup("source.path").String()
	fmt.Println(sourcePath)

	if err != nil {
		return nil, err
	}

	destPath, err := v.Lookup("dest").String()

	if err != nil {
		return nil, err
	}

	outputState := inputState.File(
		llb.Copy(
			sourceState,
			sourcePath,
			destPath,
			// FIXME: allow more configurable llb options
			// For now we define the following convenience presets:
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
				CreateDestPath:      true,
				AllowWildcard:       true,
			},
		),
		withCustomName(v, "Copy %s %s", sourcePath, destPath),
	)

	result, err := s.Solve(ctx, outputState, pctx.Platform.Get())

	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)

	output := compiler.NewValue()

	if err := output.FillPath(cue.ParsePath("output"), fs.MarshalCUE()); err != nil {
		return nil, err
	}

	return output, nil
}
