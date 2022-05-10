package task

import (
	"context"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("RmFile", func() Task { return &rmFileTask{} })
}

type rmFileTask struct {
}

func (r *rmFileTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}

	inputState, err := input.State()
	if err != nil {
		return nil, err
	}

	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	var rmOption struct {
		AllowWildcard bool
		AllowNotFound bool
	}

	if err := v.Decode(&rmOption); err != nil {
		return nil, err
	}

	outputState := inputState.File(
		llb.Rm(
			path,
			llb.WithAllowWildcard(rmOption.AllowWildcard),
			llb.WithAllowNotFound(rmOption.AllowNotFound),
		),
		withCustomName(v, "RmFile %s", path),
	)

	result, err := s.Solve(ctx, outputState, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	outputFS := pctx.FS.New(result)

	output := compiler.NewValue()
	if err := output.FillPath(cue.ParsePath("output"), outputFS.MarshalCUE()); err != nil {
		return nil, err
	}

	return output, nil
}
