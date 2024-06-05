package main

type MyModule struct{}

// Return a directory
func (m *MyModule) GetDir() *Directory {
	return m.Base().
		Directory("/src")
}

// Return a file
func (m *MyModule) GetFile() *File {
	return m.Base().
		File("/src/foo")
}

// Return a base container
func (m *MyModule) Base() *Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"mkdir", "/src"}).
		WithExec([]string{"touch", "/src/foo", "/src/bar"})
}
