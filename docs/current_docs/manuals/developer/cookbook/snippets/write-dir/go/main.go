package main

import (
	"context"
)

type MyModule struct{}

// Returns a container with a specified directory
func (m *MyModule) WriteDirectory(ctx context.Context, d *Directory) *Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", d)
}
