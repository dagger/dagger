package main

import (
	"context"

	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	return &Test{
		Ctr: dag.Container().
			From("alpine:3.22.1").
			WithExec([]string{"apk", "add", "curl"}).
			WithDefaultTerminalCmd([]string{"/bin/sh"}),
	}
}

type Test struct {
	Ctr *dagger.Container
}
