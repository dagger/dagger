package task

import (
	"context"
	"fmt"
	"io/fs"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("WriteFile", func() Task { return &writeFileTask{} })
}

type writeFileTask struct {
}

func (t *writeFileTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (TaskResult, error) {
	var contents []byte
	var err error

	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	contentsVal := v.Lookup("contents")
	switch kind := contentsVal.Kind(); kind {
	// TODO: support bytes?
	// case cue.BytesKind:
	// 	contents, err = v.Lookup("contents").Bytes()
	case cue.StringKind:
		var str string
		str, err = contentsVal.String()
		if err == nil {
			contents = []byte(str)
		}
	case cue.BottomKind:
		err = fmt.Errorf("%s: WriteFile contents is not set:\n\n%s", path, compiler.Err(contentsVal.Cue().Err()))
	default:
		err = fmt.Errorf("%s: unhandled data type in WriteFile: %s", path, kind)
	}

	if err != nil {
		return nil, err
	}

	permissions, err := v.Lookup("permissions").Int64()
	if err != nil {
		return nil, err
	}

	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}

	inputState, err := input.State()
	if err != nil {
		return nil, err
	}

	outputState := inputState.File(
		llb.Mkfile(path, fs.FileMode(permissions), contents),
		withCustomName(v, "WriteFile %s", path),
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
