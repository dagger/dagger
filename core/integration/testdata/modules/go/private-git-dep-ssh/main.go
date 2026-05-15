package main

import "context"

type Test struct{}

func (*Test) Fn(ctx context.Context) (string, error) {
	return dag.TopLevel().Fn(ctx)
}
