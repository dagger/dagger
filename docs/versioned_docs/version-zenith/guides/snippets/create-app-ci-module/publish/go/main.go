package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
)

type MyModule struct{}

// publish an image
func (m *MyModule) Publish(ctx context.Context, source *Directory) (string, error) {
	return m.Package(source).
		Publish(ctx, fmt.Sprintf("ttl.sh/myapp-%.0f:10m", math.Floor(rand.Float64()*10000000))) //#nosec
}

// create a production image
func (m *MyModule) Package(source *Directory) *Container {
	return dag.Container().From("nginx:1.25-alpine").
		WithDirectory("/usr/share/nginx/html", m.Build(source)).
		WithExposedPort(80)
}

// create a production build
func (m *MyModule) Build(source *Directory) *Directory {
	return dag.Node().WithContainer(m.buildBaseImage(source)).
		Build().
		Container().
		Directory("./dist")
}

// run unit tests
func (m *MyModule) Test(ctx context.Context, source *Directory) (string, error) {
	return dag.Node().WithContainer(m.buildBaseImage(source)).
		Run([]string{"run", "test:unit", "run"}).
		Stdout(ctx)
}

// build base image
func (m *MyModule) buildBaseImage(source *Directory) *Container {
	return dag.Node().
		WithVersion("21").
		WithNpm().
		WithSource(source).
		Install(nil).
		Container()
}
