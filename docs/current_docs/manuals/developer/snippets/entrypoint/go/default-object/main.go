package main

import (
	"context"
)

func New(
	// +optional
	ctr *Container,
) *MyModule {
	if ctr == nil {
		ctr = dag.Container().From("alpine:3.14.0")
	}
	return &MyModule{
		Ctr: *ctr,
	}
}

type MyModule struct {
	Ctr Container
}

func (m *MyModule) Version(ctx context.Context) (string, error) {
	c := m.Ctr
	return c.
		WithExec([]string{"/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"}).
		Stdout(ctx)
}
