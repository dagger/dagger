package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) ReadDir(
	ctx context.Context,
	// +defaultPath="/"
	source *dagger.Directory,
) ([]string, error) {
	return source.Entries(ctx)
}

func (m *MyModule) ReadFile(
	ctx context.Context,
	// +defaultPath="/README.md"
	source *dagger.File,
) (string, error) {
	return source.Contents(ctx)
}
