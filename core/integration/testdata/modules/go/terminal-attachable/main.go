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
			WithWorkdir("/coolworkdir").
			WithDefaultTerminalCmd([]string{"/bin/sh"}),
	}
}

type Test struct {
	Ctr *dagger.Container
}

func (t *Test) Debug() *dagger.Container {
	return t.Ctr.WithEnvVariable("COOLENV", "xoo").Terminal().WithEnvVariable("COOLENV", "yoo")
}
