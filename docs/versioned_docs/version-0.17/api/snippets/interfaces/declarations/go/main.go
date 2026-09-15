package main

import (
	"context"
)

type MyModule struct{}

type Fooer interface {
	DaggerObject
	Foo(ctx context.Context, bar int) (string, error)
}

func (m *MyModule) Foo(ctx context.Context, fooer Fooer) (string, error) {
	return fooer.Foo(ctx, 42)
}
