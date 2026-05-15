package main

import (
	"context"
	"dagger/test/internal/dagger"
)

func New(source *dagger.Directory) *Test {
	return &Test{Source: source}
}

type Test struct {
	Source *dagger.Directory
}

func (m *Test) Container() *dagger.Container {
	return dag.Container().From("alpine:3.22.1").WithDirectory("/src", m.Source)
}

func (m *Test) Conflict(ctx context.Context, mod *dagger.Module) (string, error) {
	return mod.Name(ctx)
}
