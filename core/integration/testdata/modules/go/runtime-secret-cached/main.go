package main

import (
	"context"
	"fmt"
)

type Test struct{}

func (m *Test) Foo(ctx context.Context) (string, error) {
	return m.impl(ctx, "foo")
}

func (m *Test) Bar(ctx context.Context) (string, error) {
	return m.impl(ctx, "bar")
}

func (m *Test) impl(ctx context.Context, name string) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", fmt.Sprintf("(echo %s && cat /mnt/secret) | tr [a-z] [A-Z]", name)}).
		Stdout(ctx)
}
