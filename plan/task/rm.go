package task

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Rm", func() Task { return &rmTask{} })
}

type rmTask struct {
}

func (r *rmTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (TaskResult, error) {
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

		// FIXME(TomChv): Not correctly supported by buildkit for now
		// See https://github.com/dagger/dagger/issues/2408#issuecomment-1122381170
		// AllowNotFound bool
	}

	if err := v.Decode(&rmOption); err != nil {
		return nil, err
	}

	outputState := inputState.File(
		llb.Rm(
			path,
			llb.WithAllowWildcard(rmOption.AllowWildcard),
		),
		withCustomName(v, "RmFile %s", path),
	)

	result, err := s.Solve(ctx, outputState, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	outputFS := pctx.FS.New(result)

	return TaskResult{
		"output": outputFS.MarshalCUE(),
	}, nil
}
