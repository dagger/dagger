package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (t *Test) GetFile(ctx context.Context, filename string) (string, error) {
	return dag.CurrentModule().Source().File(filename).Contents(ctx)
}

func (t *Test) GetFileAt(ctx context.Context, filename string, dir *dagger.Directory) (string, error) {
	return dir.File(filename).Contents(ctx)
}

func (t *Test) GetFileContext(
	ctx context.Context,
	filename string,
	// +defaultPath="."
	dir *dagger.Directory,
) (string, error) {
	return dir.File(filename).Contents(ctx)
}
