package main

import (
	"context"

	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	return &Test{
		Dir: dag.
			Directory().
			WithNewFile("test", "hello world\n"),
	}
}

type Test struct {
	Dir *dagger.Directory
}
