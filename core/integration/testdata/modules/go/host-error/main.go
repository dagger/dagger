package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context) *dagger.Directory {
	return dag.Host().Directory(".")
}
