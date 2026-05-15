package main

import "context"

type Test struct{}

func (m *Test) Test(ctx context.Context) (string, error) {
	return dag.Dep().Ctl(ctx, "foo")
}
