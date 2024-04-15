package main

import (
	"context"
)

type MyModule struct{}

// create a production build
func (m *MyModule) Build(source *Directory) *Directory {
	return dag.Node(NodeOpts{Ctr: m.buildBaseImage(source)}).
		Commands().
		Build().
		Directory("./dist")
}

// run unit tests
func (m *MyModule) Test(ctx context.Context, source *Directory) (string, error) {
	return dag.Node(NodeOpts{Ctr: m.buildBaseImage(source)}).
		Commands().
		Run([]string{"run", "test:unit", "run"}).
		Stdout(ctx)
}

// build base image
func (m *MyModule) buildBaseImage(source *Directory) *Container {
	return dag.Node(NodeOpts{Version: "21"}).
		WithNpm().
		WithSource(source).
		Install(nil).
		Container()
}
