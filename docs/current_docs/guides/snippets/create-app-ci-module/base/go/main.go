package main

type MyModule struct{}

// build base image
func (m *MyModule) buildBaseImage(source *Directory) *Container {
	return dag.Node(NodeOpts{Version: "21"}).
		WithNpm().
		WithSource(source).
		Install(nil).
		Container()
}
