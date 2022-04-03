package task

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Merge", func() Task { return &mergeTask{} })
}

type mergeTask struct{}

func (t *mergeTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	inputs, err := v.Lookup("inputs").List()
	if err != nil {
		return nil, err
	}

	inputStates := make([]llb.State, len(inputs))
	for i, input := range inputs {
		inputFS, err := pctx.FS.FromValue(input)
		if err != nil {
			return nil, err
		}
		inputState, err := inputFS.State()
		if err != nil {
			return nil, err
		}
		inputStates[i] = inputState
	}

	st := llb.Merge(inputStates)
	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}
