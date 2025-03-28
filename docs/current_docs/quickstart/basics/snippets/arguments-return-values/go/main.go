package main

import (
	"context"
	"dagger/basics/internal/dagger"
)

type Basics struct{}

func (m *Basics) FooBuild(
	// +default "alpine:latest"
	image string,
) *dagger.Container {
	return dag.Container().
		From(image).
		WithNewFile("/hi.txt", "Hello from Dagger!")
}

func (m *Basics) FooPublish(
	ctx context.Context,
	// +default "alpine:latest"
	image string,
) (string, error) {
	return m.FooBuild(image).
		WithEntrypoint([]string{"cat", "/hi.txt"}).
		Publish(ctx, "ttl.sh/hello")
}
