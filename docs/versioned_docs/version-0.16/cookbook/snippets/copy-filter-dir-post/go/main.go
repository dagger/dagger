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
	// Directory exclusion pattern
	// +optional
	excludeDirectoryPattern string,
	// +optional
	// File exclusion pattern
	excludeFilePattern string,
) *dagger.Container {
	filteredSource := source.
		WithoutDirectory(excludeDirectoryPattern).
		WithoutFile(excludeFilePattern)
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", filteredSource)
}
