package main

import (
	"context"
	"fmt"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context, sockID string) error {
	_, err := dag.Container().From("alpine:3.22.1").
		WithExec([]string{"apk", "add", "netcat-openbsd"}).
		WithUnixSocket("/var/run/host.sock", dagger.Ref[*dagger.Socket](dag, dagger.ID(sockID))).
		WithExec([]string{"nc", "-w", "5", "-U", "/var/run/host.sock"}).
		Stdout(ctx)
	if err == nil {
		return fmt.Errorf("expected error, got nil")
	}
	return nil
}
