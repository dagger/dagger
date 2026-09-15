package main

import "context"

type Test struct{}

func (m *Test) Foo(ctx context.Context) (string, error) {
	return dag.Dep().Bar(ctx)
}
