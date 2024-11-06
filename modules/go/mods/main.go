package main

import (
	"dagger/mods/internal/dagger"
)

type Mods struct{}

// Returns a container that echoes whatever string argument is provided
func (m *Mods) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}
