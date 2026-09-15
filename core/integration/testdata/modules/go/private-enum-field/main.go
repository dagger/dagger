package main

import (
	"context"
)

type Test struct{}

func (m Test) Publish(ctx context.Context) (string, error) {
	return dag.Dep().Publish(ctx)
}
