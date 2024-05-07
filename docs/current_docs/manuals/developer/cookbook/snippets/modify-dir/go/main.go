package main

import (
	"context"
)

type MyModule struct{}

// Returns a container with a specified directory and an additional file
func (m *MyModule) ModifyDirectory(ctx context.Context, dir *Directory) *Container {
	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", dir).
		WithExec([]string{"/bin/sh", "-c", `echo foo > /src/foo`})
}
