package main

import "dagger/outside-root/internal/dagger"

type OutsideRoot struct{}

func (m *OutsideRoot) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"echo", stringArg})
}
