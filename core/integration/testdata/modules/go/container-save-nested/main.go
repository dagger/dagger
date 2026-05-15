package main

import "context"

type Test struct{}

func (m *Test) Try(ctx context.Context) error {
	return dag.Container().
		From("alpine").
		WithExec([]string{"touch", "/foo"}).
		ExportImage(ctx, "foobar:latest")
}
