package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ReadFile(
	ctx context.Context,
	// +defaultPath="."
	dir *dagger.Directory,
) (string, error) {
	return dir.File("test-file.txt").Contents(ctx)
}
