package main

import (
	"context"
)

type MyModule struct{}

// Returns a container with a specified directory and an additional file
func (m *MyModule) CopyAndModifyDirectory(ctx context.Context, d *Directory) *Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", d).
		WithExec([]string{"/bin/sh", "-c", `echo foo > /src/foo`})
}
