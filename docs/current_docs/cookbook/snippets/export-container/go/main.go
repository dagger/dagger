package main

type MyModule struct{}

// Return a container
func (m *MyModule) Base() *Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"mkdir", "/src"}).
		WithExec([]string{"touch", "/src/foo", "/src/bar"})
}
