package main

import "dagger/foo/internal/dagger"

type Foo struct{}

func (m *Foo) ContainerEcho(stringArg string) *dagger.Container {
	return dag.Container().From("alpine:3.22.1").WithExec([]string{"echo", stringArg})
}
