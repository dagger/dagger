package main

import "context"

type MyModule struct{}

func (m *MyModule) Foo(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"sh", "-c", "echo hello world > /foo"}).
		WithExec([]string{"cat", "/FOO"}). // deliberate error
		Stdout(ctx)
}

// run with dagger call --interactive foo
