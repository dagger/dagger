package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) CallDep(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().Test().Sync(ctx)
}

func (m *Test) CallDepFile(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().TestFile().Sync(ctx)
}
