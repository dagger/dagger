package main

import (
	"context"
	"dagger/hello-with-container/internal/dagger"
)

type HelloWithContainer struct{}

// TestWithDefaultContainer uses alpine:3.19 when no container is provided
func (m *HelloWithContainer) TestWithDefaultContainer(
	ctx context.Context,
	// +defaultAddress="alpine:3.19"
	ctr *dagger.Container,
) (string, error) {
	return ctr.WithExec([]string{"cat", "/etc/alpine-release"}).Stdout(ctx)
}
