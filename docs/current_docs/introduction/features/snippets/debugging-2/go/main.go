package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Foo(ctx context.Context) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		Terminal().
		WithExec([]string{"sh", "-c", "echo hello world > /foo"}).
		Terminal()
}
