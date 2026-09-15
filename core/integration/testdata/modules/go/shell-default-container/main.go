package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Container() *dagger.Container {
	return dag.Container().From("alpine:3.22.1")
}
