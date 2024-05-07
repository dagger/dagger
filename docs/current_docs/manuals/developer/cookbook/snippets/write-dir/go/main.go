package main

import (
	"context"
)

type MyModule struct{}

// Returns a container with a specified directory
func (m *MyModule) WriteDirectory(ctx context.Context, dir *Directory) *Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", dir)
}
