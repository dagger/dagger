package main

import "context"

type Test struct{}

func (m *Test) UseHello(ctx context.Context) (string, error) {
	return dag.Dep().Hello(ctx)
}
