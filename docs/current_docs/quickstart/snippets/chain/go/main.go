package main

import (
	"context"
)

type HelloDagger struct{}

// Returns a directory
func (m *HelloDagger) Foo() *Directory {
	return dag.Container().From("cgr.dev/chainguard/wolfi-base").Directory("/")
}

// Returns entries in a directory
func (m *HelloDagger) Bar(ctx context.Context) ([]string, error) {
	return m.Foo().Entries(ctx)
}
