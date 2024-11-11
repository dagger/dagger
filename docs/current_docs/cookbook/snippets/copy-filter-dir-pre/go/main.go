package main

import (
	"context"
	"main/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) CopyDirectoryWithExclusions(
	ctx context.Context,
	// +ignore=["*", "!**/*.md"]
	source *dagger.Directory,
) (*dagger.Container, error) {
	return dag.
		Container().
		From("alpine:latest").
		WithDirectory("/src", source).
		Sync(ctx)
}
