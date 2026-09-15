package main

import "context"

type B struct{}

func (m *B) Fn(ctx context.Context, foo int) (int, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
