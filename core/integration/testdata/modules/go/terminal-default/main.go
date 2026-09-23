package main

import (
	"context"

	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	return &Test{
		Ctr: dag.Container().
			From("alpine:3.22.1").
			WithEnvVariable("COOLENV", "woo").
			WithWorkdir("/coolworkdir"),
		Src: dag.Directory().WithNewFile("src.txt", "src\n"),
	}
}

type Test struct {
	Ctr *dagger.Container
	Src *dagger.Directory
}
