package main

type MyModule struct{}

// build base image
func (m *MyModule) buildBaseImage() *Node {
	return dag.Node().
		WithVersion("21").
		WithNpm().
		WithSource(dag.CurrentModule().Source(".", HostDirectoryOpts{
			Exclude: []string{".git", "**/node_modules"},
		})).
		Install(nil)
}
