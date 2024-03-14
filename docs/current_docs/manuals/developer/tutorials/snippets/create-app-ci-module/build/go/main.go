package main

import (
	"context"
)

type MyModule struct{}

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