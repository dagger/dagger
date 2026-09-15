// A generated module for Test functions

package main

import "dagger/test/internal/dagger"

type Test struct{}

// Returns a container that echoes whatever string argument is provided
func (m *Test) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"echo", stringArg})
}
