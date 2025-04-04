package main

import (
	"context"
	"fmt"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Return a container with a mounted file
func (m *MyModule) MountFile(
	ctx context.Context,
	// Source file
	f *dagger.File,
) *dagger.Container {
	name, _ := f.Name(ctx)
	return dag.Container().
		From("alpine:latest").
		WithMountedFile(fmt.Sprintf("/src/%s", name), f)
}
