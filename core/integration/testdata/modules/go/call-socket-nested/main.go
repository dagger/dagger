package main

import (
	"context"
	"dagger/test/internal/dagger"
	"fmt"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, sock *dagger.Socket) error {
	out, err := dag.Container().From("alpine:3.22.1").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", sock).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if out != "yoyoyo" {
		return fmt.Errorf("unexpected output: %s", out)
	}
	return nil
}
