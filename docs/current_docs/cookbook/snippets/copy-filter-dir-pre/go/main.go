package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Return a container with a filtered directory
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
