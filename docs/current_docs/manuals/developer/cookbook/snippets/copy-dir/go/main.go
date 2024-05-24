package main

import (
	"context"
)

type MyModule struct{}

// Return a container with a specified directory
func (m *MyModule) CopyDirectory(ctx context.Context, d *Directory) *Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", d)
}
