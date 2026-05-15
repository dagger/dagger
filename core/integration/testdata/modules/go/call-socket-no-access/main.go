package main

import (
	"context"
	"dagger/test/internal/dagger"
	"fmt"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, sockID string) error {
	_, err := dag.Container().From("alpine:3.22.1").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", dag.LoadSocketFromID(dagger.SocketID(sockID))).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"}).
		Stdout(ctx)
	if err == nil {
		return fmt.Errorf("expected error, got nil")
	}
	return nil
}
