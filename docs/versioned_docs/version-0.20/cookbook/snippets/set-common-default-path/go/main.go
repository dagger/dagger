package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct {
	Source *dagger.Directory
}

func New(
	// +defaultPath="."
	source *dagger.Directory,
) *MyModule {
	return &MyModule{
		Source: source,
	}
}

func (m *MyModule) Foo(ctx context.Context) ([]string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/app", m.Source).
		Directory("/app").
		Entries(ctx)
}
