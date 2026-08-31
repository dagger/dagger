package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Return a container with a specified directory
func (m *MyModule) CopyDirectory(
	ctx context.Context,
	// Source directory
	source *dagger.Directory,
) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", source)
}
