package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
)

type HelloDagger struct{}

// Tests, builds, packages and publishes the application
func (m *HelloDagger) Ci(ctx context.Context, source *Directory) (string, error) {
	// run tests
	_, err := m.Test(ctx, source)
	if err != nil {
		return "", err
	}
	// obtain the build output directory
	build := m.Build(source)
	// create and publish a container with the build output
	address, err := m.Package(build).
		Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		return "", err
	}
	return address, nil
}

// Returns a container with the production build
func (m *HelloDagger) Package(build *Directory) *Container {
	return dag.Container().From("nginx:1.25-alpine").
		WithDirectory("/usr/share/nginx/html", build).
		WithExposedPort(80)
}

// Returns a directory with the production build
func (m *HelloDagger) Build(source *Directory) *Directory {
	nodeCache := dag.CacheVolume("node")
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", source.WithoutDirectory("dagger")).
		WithWorkdir("/src").
		WithMountedCache("/src/node_modules", nodeCache).
		WithExec([]string{"npm", "install"}).
		WithExec([]string{"npm", "run", "build"}).
		Directory("./dist")
}

// Returns the result of running unit tests
func (m *HelloDagger) Test(ctx context.Context, source *Directory) (string, error) {
	nodeCache := dag.CacheVolume("node")
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", source.WithoutDirectory("dagger")).
		WithWorkdir("/src").
		WithMountedCache("/src/node_modules", nodeCache).
		WithExec([]string{"npm", "install"}).
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		Stdout(ctx)
}
