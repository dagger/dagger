package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Foo(
	ctx context.Context,
	source *dagger.Directory,
) *dagger.Container {
	builder := dag.
		Container().
		From("golang:latest").
		WithDirectory("/src", source, dagger.ContainerWithDirectoryOpts{Exclude: []string{"*.git", "internal"}}).
		WithWorkdir("/src/hello").
		WithExec([]string{"go", "build", "-o", "hello.bin", "."})
	return dag.
		Container().
		From("alpine:latest").
		WithDirectory("/app", builder.Directory("/src/hello"), dagger.ContainerWithDirectoryOpts{Include: []string{"hello.bin"}}).
		WithEntrypoint([]string{"/app/hello.bin"})
}
