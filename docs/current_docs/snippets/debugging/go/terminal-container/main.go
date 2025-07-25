package main

import (
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Container() *dagger.Container {
	return dag.Container().
		From("alpine:latest").
		Terminal().
		WithExec([]string{"sh", "-c", "echo hello world > /foo && cat /foo"}).
		Terminal()
}
