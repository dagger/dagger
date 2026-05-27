package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {
	Message string
}

func New() *Test {
	return &Test{Message: "hello from field"}
}

func (m *Test) ContainerEcho(
	// +optional
	// +default="Hello Self Calls"
	stringArg string,
) *dagger.Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

func (m *Test) Print(ctx context.Context, stringArg string) (string, error) {
	return dag.Test().ContainerEcho(dagger.TestContainerEchoOpts{
		StringArg: stringArg,
	}).Stdout(ctx)
}

func (m *Test) PrintDefault(ctx context.Context) (string, error) {
	return dag.Test().ContainerEcho().Stdout(ctx)
}
