package main

import "dagger/evil/internal/dagger"

type Evil struct{}

func (m *Evil) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"echo", stringArg})
}
