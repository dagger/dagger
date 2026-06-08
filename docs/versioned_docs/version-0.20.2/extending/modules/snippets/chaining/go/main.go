package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Foo(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithEntrypoint([]string{"cat", "/etc/os-release"}).
		Publish(ctx, "ttl.sh/my-alpine")
}
