package main

import (
	"context"

	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	dep := dag.Container().
		From("alpine:3.22.1").
		WithDefaultArgs([]string{"sh", "-c", "sleep 1; echo dependency crashed >&2; exit 42"}).
		AsService()

	return &Test{
		Ctr: dag.Container().
			From("alpine:3.22.1").
			WithWorkdir("/coolworkdir").
			WithServiceBinding("dep", dep),
	}
}

type Test struct {
	Ctr *dagger.Container
}
