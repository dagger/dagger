package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Version(
	ctx context.Context,
	// +defaultAddress="alpine:latest"
	ctr *dagger.Container,
) (string, error) {
	return ctr.WithExec([]string{"cat", "/etc/alpine-release"}).Stdout(ctx)
}
