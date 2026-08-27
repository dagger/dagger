package main

import (
	"context"

	"dagger/hello-dagger/internal/dagger"
)

type HelloDagger struct{}

// Returns a base container
func (m *HelloDagger) Base() *dagger.Container {
	return dag.Container().From("cgr.dev/chainguard/wolfi-base")
}

// Builds on top of base container and returns a new container
func (m *HelloDagger) Build() *dagger.Container {
	return m.Base().WithExec([]string{"apk", "add", "bash", "git"})
}

// Builds and publishes a container
func (m *HelloDagger) BuildAndPublish(ctx context.Context) (string, error) {
	return m.Build().Publish(ctx, "ttl.sh/bar")
}
