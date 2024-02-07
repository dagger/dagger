package main

type MyModule struct {
	Source *Directory
}

func New(source *Directory) *MyModule {
	return &MyModule{
		Source: source,
	}
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
