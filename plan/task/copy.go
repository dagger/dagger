package task

import (
	"context"

	"dagger.io/dagger"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Copy", func() Task { return &copyTask{} })
}

type copyTask struct {
}

func (t *copyTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var err error

	// return nil, err

	// input, err := pctx.FS.FromValue(v.Lookup("input"))
	// if err != nil {
	// 	return nil, err
	// }

	// inputState, err := input.State()
	// if err != nil {
	// 	return nil, err
	// }

	inputFsid, err := utils.GetFSId(v.Lookup("input"))

	if err != nil {
		return nil, err
	}

	contentsFsid, err := utils.GetFSId(v.Lookup("contents"))
	if err != nil {
		return nil, err
	}

	// contentsState, err := contents.State()
	// if err != nil {
	// 	return nil, err
	// }

	sourcePath, err := v.Lookup("source").String()
	if err != nil {
		return nil, err
	}

	destPath, err := v.Lookup("dest").String()
	if err != nil {
		return nil, err
	}

	var filters struct {
		Include []string
		Exclude []string
	}

	if err := v.Decode(&filters); err != nil {
		return nil, err
	}

	// FIXME: allow more configurable llb options
	// For now we define the following convenience presets.
	// opts := &llb.CopyInfo{
	// 	CopyDirContentsOnly: true,
	// 	CreateDestPath:      true,
	// 	AllowWildcard:       true,
	// 	IncludePatterns:     filters.Include,
	// 	ExcludePatterns:     filters.Exclude,
	// }

	// outputState := inputState.File(
	// 	llb.Copy(
	// 		contentsState,
	// 		sourcePath,
	// 		destPath,
	// 		opts,
	// 	),
	// 	withCustomName(v, "Copy %s %s", sourcePath, destPath),
	// )

	// result, err := s.Solve(ctx, outputState, pctx.Platform.Get())
	// if err != nil {
	// 	return nil, err
	// }

	// TODO: Filters would be nice! Also... a way to specify where the new directory should go...
	// Beyond what I've done here.

	dgr := s.Client

	sourceDirID, err := dgr.Directory(dagger.DirectoryOpts{ID: dagger.DirectoryID(contentsFsid)}).Directory(sourcePath).ID(ctx)
	if err != nil {
		return nil, err
	}

	fsid, err := dgr.Directory(dagger.DirectoryOpts{ID: dagger.DirectoryID(inputFsid)}).WithDirectory(dagger.DirectoryID(sourceDirID), destPath).ID(ctx)
	if err != nil {
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewFS(dagger.DirectoryID(fsid)),
	})
}
