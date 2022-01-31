package task

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("OutputFile", func() Task { return &outputFileTask{} })
}

type outputFileTask struct {
}

func (c outputFileTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var contents []byte
	var err error

	switch kind := v.Lookup("contents").Kind(); kind {
	case cue.StringKind:
		var str string
		str, err = v.Lookup("contents").String()
		if err == nil {
			contents = []byte(str)
		}
	case cue.BottomKind:
		err = fmt.Errorf("contents is not set")
	default:
		err = fmt.Errorf("unhandled data type in contents: %s", kind)
	}

	if err != nil {
		return nil, err
	}

	dest, err := v.Lookup("dest").AbsPath()
	if err != nil {
		return nil, err
	}

	perm := fs.FileMode(0644) // default permission
	if v.Lookup("permissions").Exists() {
		permissions, err := v.Lookup("permissions").Int64()
		if err != nil {
			return nil, err
		}
		perm = fs.FileMode(permissions)
	}

	err = os.WriteFile(dest, contents, perm)
	if err != nil {
		return nil, err
	}

	return compiler.NewValue(), nil
}
