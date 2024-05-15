package main

import (
	"context"
)

type HelloDagger struct{}

// Returns a container
func (m *HelloDagger) Foo() *Container {
	return dag.Container().From("cgr.dev/chainguard/wolfi-base")
}

// Publishes a container
func (m *HelloDagger) Bar(ctx context.Context) (string, error) {
	return m.Foo().Publish(ctx, "ttl.sh/bar")
}
