package main

import (
	"context"
)

type MyModule struct{}

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
		WithSource(dag.Host().Directory(".", HostDirectoryOpts{
			Exclude: []string{".git", "**/node_modules"},
		})).
		Install(nil)
}
