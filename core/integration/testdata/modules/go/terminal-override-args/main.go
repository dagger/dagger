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
			WithExec([]string{"apk", "add", "python3"}).
			WithDefaultTerminalCmd([]string{"/bin/sh"}),
	}
}

type Test struct {
	Ctr *dagger.Container
}
