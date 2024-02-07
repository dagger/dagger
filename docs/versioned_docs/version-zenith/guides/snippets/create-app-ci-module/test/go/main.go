package main

import (
	"context"
)

type MyModule struct {
	Source *Directory
}

func New(source *Directory) *MyModule {
	return &MyModule{
		Source: source,
	}
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
