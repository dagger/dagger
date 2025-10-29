package main

import (
	"context"

	"dagger.io/dagger/dag"
)

type MyModule struct{}

func (m *MyModule) Foo(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithEntrypoint([]string{"cat", "/etc/os-release"}).
		Publish(ctx, "ttl.sh/my-alpine")
}
