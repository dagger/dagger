package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	return dag.Dep().Fn(ctx, sock)
}
