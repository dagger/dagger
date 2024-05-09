package main

import (
	"context"
)

type HelloDagger struct{}

// Returns a container with the production build
func (m *HelloDagger) Package(source *Directory) *Container {
	return dag.Container().From("nginx:1.25-alpine").
		WithDirectory("/usr/share/nginx/html", m.Build(source)).
		WithExposedPort(80)
}

// Returns a directory with the production build
func (m *HelloDagger) Build(source *Directory) *Directory {
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", source.WithoutDirectory("dagger")).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"}).
		WithExec([]string{"npm", "run", "build"}).
		Directory("./dist")
}

// Returns the result of running unit tests
func (m *HelloDagger) Test(ctx context.Context, source *Directory) (string, error) {
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", source.WithoutDirectory("dagger")).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"}).
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		Stdout(ctx)
}
