package main

import "dagger/my-module/internal/dagger"

type MyModule struct{}

// Return a directory
func (m *MyModule) GetDir() *dagger.Directory {
	return m.Base().
		Directory("/src")
}

// Return a file
func (m *MyModule) GetFile() *dagger.File {
	return m.Base().
		File("/src/foo")
}

// Return a base container
func (m *MyModule) Base() *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"mkdir", "/src"}).
		WithExec([]string{"touch", "/src/foo", "/src/bar"})
}
