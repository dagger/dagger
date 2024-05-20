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
	build := dag.Node(NodeOpts{Ctr: m.BuildEnv(source)}).
		Commands().
		Run([]string{"build"}).
		Directory("./dist")
	return dag.Container().From("nginx:1.25-alpine").
		WithDirectory("/usr/share/nginx/html", build).
		WithExposedPort(80)
}

// Returns the result of running unit tests
func (m *HelloDagger) Test(ctx context.Context, source *Directory) (string, error) {
	return dag.Node(NodeOpts{Ctr: m.BuildEnv(source)}).
		Commands().
		Run([]string{"test:unit", "run"}).
		Stdout(ctx)
}

// Returns a container with the build environment
func (m *HelloDagger) BuildEnv(source *Directory) *Container {
	return dag.Node(NodeOpts{Version: "21"}).
		WithNpm().
		WithSource(source).
		Install(nil).
		Container()
}
