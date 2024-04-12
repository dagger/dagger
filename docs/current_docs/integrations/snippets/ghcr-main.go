package main

import (
	"context"

	"dagger.io/dagger/dag"
)

type Cowsay struct{}

// Application specific build logic
func (m *Cowsay) Build(ctx context.Context, buildContext *Directory) *Container {
	return dag.Container().
		From("ubuntu:latest").
		WithFile("/cow.txt", buildContext.File("cow.txt")).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "cowsay"}).
		WithEntrypoint([]string{"bash", "-c", "/usr/games/cowsay < /cow.txt"})
}

// Take the built container and push it
func (m *Cowsay) BuildAndPush(ctx context.Context, registry, imageName, username string, password *Secret, buildContext *Directory) error {
	_, err := m.Build(ctx, buildContext).
		WithRegistryAuth(registry, username, password).
		Publish(ctx, registry+"/"+imageName)
	return err
}
