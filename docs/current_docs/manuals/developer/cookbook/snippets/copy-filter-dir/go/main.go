package main

import (
	"context"
)

type MyModule struct{}

// Return a container with a filtered directory
func (m *MyModule) CopyDirectoryWithExclusions(
	ctx context.Context,
	// Source directory
	source *Directory,
	// Directory exclusion pattern
	// +optional
	excludeDirectoryPattern string,
	// +optional
	// File exclusion pattern
	excludeFilePattern string,
) *Container {
	filteredSource := source.
		WithoutDirectory(excludeDirectoryPattern).
		WithoutFile(excludeFilePattern)
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", filteredSource)
}
