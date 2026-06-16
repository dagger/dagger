package main

import "context"

type Test struct{}

var someDefault = dag.Container().From("alpine:3.22.1")

func (m *Test) Fn(ctx context.Context) (string, error) {
	return someDefault.WithExec([]string{"echo", "foo"}).Stdout(ctx)
}
