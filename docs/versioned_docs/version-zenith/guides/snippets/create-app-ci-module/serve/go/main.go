package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
)

type MyModule struct {
	Source *Directory
}

func New(source *Directory) *MyModule {
	return &MyModule{
		Source: source,
	}
}

// create a service from the production image
func (m *MyModule) Serve() *Service {
	return m.Package().AsService()
}

// publish an image
func (m *MyModule) Publish(ctx context.Context) (string, error) {
	return m.Package().
		Publish(ctx, fmt.Sprintf("ttl.sh/myapp-%.0f:10m", math.Floor(rand.Float64()*10000000))) //#nosec
}

// create a production image
func (m *MyModule) Package() *Container {
	return dag.Container().From("nginx:1.25-alpine").
		WithDirectory("/usr/share/nginx/html", m.Build()).
		WithExposedPort(80)
}

// create a production build
func (m *MyModule) Build() *Directory {
	return dag.Node().WithContainer(m.buildBaseImage()).
		Build().
		Container().
		Directory("./dist")
}

// run unit tests
func (m *MyModule) Test(ctx context.Context) (string, error) {
	return dag.Node().WithContainer(m.buildBaseImage()).
		Run([]string{"run", "test:unit", "run"}).
		Stdout(ctx)
}

// build base image
func (m *MyModule) buildBaseImage() *Container {
	return dag.Node().
		WithVersion("21").
		WithNpm().
		WithSource(m.Source).
		Install(nil).
		Container()
}
