package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Ctr() *dagger.Container {
	return dag.Container().
		From("alpine:3.22.1").
		WithDefaultArgs([]string{"echo", "hello"}).
		WithExec([]string{})
}

func (m *Test) Fail() *dagger.Container {
	return dag.Container().
		From("alpine:3.22.1").
		WithExec([]string{"sh", "-c", "echo goodbye; exit 127"})
}
