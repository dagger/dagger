package main

import (
	"context"
)

type MyModule struct{}

// Return a container with a specified directory
func (m *MyModule) CopyDirectory(
	ctx context.Context,
	// Source directory
	source *Directory,
) *Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", source)
}
