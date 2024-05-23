package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

// Returns a container with a specified file
func (m *MyModule) CopyFile(ctx context.Context, f *File) *Container {
	name, _ := f.Name(ctx)
	return dag.Container().
		From("alpine:latest").
		WithFile(fmt.Sprintf("/src/%s", name), f)
}
