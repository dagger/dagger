package main

import (
	"context"
)

type MyModule struct{}

// create a production build
func (m *MyModule) Build() *Directory {
	return m.buildBaseImage().
		Build().
		Container().
		Directory("./dist")
}

// run unit tests
func (m *MyModule) Test(ctx context.Context) (string, error) {
	return m.buildBaseImage().
		Run([]string{"run", "test:unit", "run"}).
		Stdout(ctx)
}

// build base image
func (m *MyModule) buildBaseImage() *Node {
	return dag.Node().
		WithVersion("21").
		WithNpm().
		WithSource(dag.CurrentModule().Source()).
		Install(nil)
}
