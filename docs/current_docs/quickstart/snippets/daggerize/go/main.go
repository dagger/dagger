package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
)

type HelloDagger struct{}

// Tests, builds and publishes the application
func (m *HelloDagger) Publish(ctx context.Context, source *Directory) (string, error) {
	_, err := m.Test(ctx, source)
	if err != nil {
		return "", err
	}
	address, err := m.Build(source).
		Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		return "", err
	}
	return address, nil
}

// Returns a container with the production build and an NGINX service
func (m *HelloDagger) Build(source *Directory) *Container {
	build := m.BuildEnv(source).
		WithExec([]string{"npm", "run", "build"}).
		Directory("./dist")
	return dag.Container().From("nginx:1.25-alpine").
		WithDirectory("/usr/share/nginx/html", build).
		WithExposedPort(80)
}

// Returns the result of running unit tests
func (m *HelloDagger) Test(ctx context.Context, source *Directory) (string, error) {
	return m.BuildEnv(source).
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		Stdout(ctx)
}

// Returns a container with the build environment
func (m *HelloDagger) BuildEnv(source *Directory) *Container {
	nodeCache := dag.CacheVolume("node")
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", source).
		WithMountedCache("/src/node_modules", nodeCache).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"})
}
