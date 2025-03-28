package main

import (
	"context"
)

type Basics struct{}

func (m *Basics) Foo(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithNewFile("/hi.txt", "Hello from Dagger!").
		WithEntrypoint([]string{"cat", "/hi.txt"}).
		Publish(ctx, "ttl.sh/hello")
}
