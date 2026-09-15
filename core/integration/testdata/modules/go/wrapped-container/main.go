package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Container() *WrappedContainer {
	return &WrappedContainer{
		dag.Container().From("alpine:3.22.1"),
	}
}

type WrappedContainer struct {
	Unwrap *dagger.Container `json:"unwrap"`
}

func (c *WrappedContainer) Echo(msg string) *WrappedContainer {
	return &WrappedContainer{
		c.Unwrap.WithExec([]string{"echo", "-n", msg}),
	}
}
