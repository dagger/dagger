package main

import (
	"context"

	"dagger/test/internal/dagger"
)

func New(ctx context.Context, nestedSrc *dagger.Directory) *Test {
	return &Test{
		Ctr: dag.Container().
			From("golang:1.26-alpine").
			WithMountedDirectory("/src", nestedSrc).
			WithWorkdir("/src").
			WithDefaultTerminalCmd([]string{"go", "run", "."}),
	}
}

type Test struct {
	Ctr *dagger.Container
}
