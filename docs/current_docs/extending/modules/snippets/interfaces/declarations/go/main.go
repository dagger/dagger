package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

type Fooer interface {
	DaggerObject
	Foo(ctx context.Context, bar int) (string, error)
}

func (m *MyModule) Foo(ctx context.Context, fooer Fooer) (string, error) {
	return fooer.Foo(ctx, 42)
}
