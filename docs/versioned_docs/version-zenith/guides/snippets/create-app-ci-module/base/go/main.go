package main

import (
	"context"
)

type MyModule struct{}

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
