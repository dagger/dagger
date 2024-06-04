package main

import "context"

func New(
	// +default=dag.container().from("alpine:3.14.0")
	ctr Container,
) *MyModule {
	return &MyModule{
		Ctr: ctr,
	}
}

type MyModule struct {
	Ctr Container
}

func (m *MyModule) Version(ctx context.Context) string {
	return m.Ctr.
		WithExec([]string{"/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"}).
		Stdout(ctx)
}
