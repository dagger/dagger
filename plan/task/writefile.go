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
	Register("WriteFile", func() Task { return &writeFileTask{} })
}

type writeFileTask struct {
}

func (t *writeFileTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var str string
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
		str, err = contentsVal.String()
	case cue.BottomKind:
		err = fmt.Errorf("%s: WriteFile contents is not set:\n\n%s", path, compiler.Err(contentsVal.Cue().Err()))
	default:
		err = fmt.Errorf("%s: unhandled data type in WriteFile: %s", path, kind)
	}

	if err != nil {
		return nil, err
	}

	// permissions, err := v.Lookup("permissions").Int64()
	// if err != nil {
	// 	return nil, err
	// }

	fsid, err := utils.GetFSId(v.Lookup("input"))

	if err != nil {
		return nil, err
	}

	dgr := s.Client

	newFSID, err := dgr.Directory(dagger.DirectoryOpts{ID: dagger.DirectoryID(fsid)}).WithNewFile(path, dagger.DirectoryWithNewFileOpts{
		Contents: str,
	}).ID(ctx)

	if err != nil {
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewFS(dagger.DirectoryID(newFSID)),
	})
}
