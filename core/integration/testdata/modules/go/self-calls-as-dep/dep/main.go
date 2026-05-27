package main

import (
	"context"

	"dagger/dep/internal/dagger"
)

func New() *Dep {
	return &Dep{
		Base: dag.Container().From("alpine:latest"),
	}
}

type Dep struct {
	Base *dagger.Container
}

func (m *Dep) ContainerEcho(stringArg string) *dagger.Container {
	return m.Base.WithExec([]string{"echo", stringArg})
}

func (m *Dep) Print(ctx context.Context, stringArg string) (string, error) {
	return dag.Dep().ContainerEcho(stringArg).Stdout(ctx)
}

func (m *Dep) ViaSelfContainer(stringArg string) *dagger.Container {
	return dag.Dep().ContainerEcho(stringArg)
}

func (m *Dep) Worker() *Worker {
	return &Worker{}
}

type Worker struct{}

func (w *Worker) Echo(ctx context.Context, stringArg string) (string, error) {
	return dag.Dep().ContainerEcho(stringArg).Stdout(ctx)
}
