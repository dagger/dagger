package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Return a container with a filtered directory
func (m *MyModule) CopyDirectoryWithExclusions(
	ctx context.Context,
	// Source directory
	source *dagger.Directory,
	// Exclusion pattern
	// +optional
	exclude []string,
) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", source, dagger.ContainerWithDirectoryOpts{Exclude: exclude})
}

