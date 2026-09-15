package main

import "context"

type Test struct{}

func (*Test) Fn(ctx context.Context, rand string) (string, error) {
	return dag.Dep().Fn(ctx, rand)
}
