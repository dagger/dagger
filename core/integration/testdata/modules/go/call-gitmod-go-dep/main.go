package main

import "context"

type Foo struct{}

func (m *Foo) Fn(ctx context.Context) (string, error) {
	return dag.TopLevel().Fn(ctx)
}
