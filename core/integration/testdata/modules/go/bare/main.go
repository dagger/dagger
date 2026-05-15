package main

import "dagger/bare/internal/dagger"

type Bare struct{}

func (m *Bare) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"echo", stringArg})
}
