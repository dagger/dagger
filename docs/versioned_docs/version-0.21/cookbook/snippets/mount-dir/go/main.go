package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Return a container with a mounted directory
func (m *MyModule) MountDirectory(
	ctx context.Context,
	// Source directory
	source *dagger.Directory,
) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/src", source)
}
