package main

import "context"

type C struct{}

func (m *C) Fn(ctx context.Context, foo string) (string, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
