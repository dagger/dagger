package main

import (
	"context"
)

type MyModule struct{}

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
