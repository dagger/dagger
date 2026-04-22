package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

func New(
	// +optional
	ctr *dagger.Container,
) *MyModule {
	if ctr == nil {
		ctr = dag.Container().From("alpine:3.14.0")
	}
	return &MyModule{
		Ctr: *ctr,
	}
}

type MyModule struct {
	Ctr dagger.Container
}

func (m *MyModule) Version(ctx context.Context) (string, error) {
	c := m.Ctr
	return c.
		WithExec([]string{"/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"}).
		Stdout(ctx)
}
