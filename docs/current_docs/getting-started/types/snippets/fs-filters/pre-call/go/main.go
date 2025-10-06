package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Foo(
	ctx context.Context,
	// +ignore=["*", "!**/*.go", "!go.mod", "!go.sum"]
	source *dagger.Directory,
) (*dagger.Container, error) {
	return dag.
		Container().
		From("alpine:latest").
		WithDirectory("/src", source).
		Sync(ctx)
}
