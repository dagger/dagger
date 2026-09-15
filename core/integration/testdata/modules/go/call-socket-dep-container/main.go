package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	ctr := dag.Container().From("alpine:3.22.1").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", sock).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"})
	return dag.Dep().Fn(ctx, ctr)
}
