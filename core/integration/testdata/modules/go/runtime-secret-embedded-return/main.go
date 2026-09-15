package main

import "context"

type Test struct{}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return dag.Dep().GetEncoded().Stdout(ctx)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	return dag.Dep().GetCensored().Stdout(ctx)
}
