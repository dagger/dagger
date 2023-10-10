package main

import "context"

type Use struct{}

func (m *Use) UseHello(ctx context.Context) (string, error) {
	return dag.Dep().Hello(ctx)
}
