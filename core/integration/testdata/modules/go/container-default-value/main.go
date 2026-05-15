package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

// TestWithDefaultContainer uses alpine:latest when no container is provided
func (t *Test) TestWithDefaultContainer(
	ctx context.Context,
	// +defaultAddress="alpine:3.19"
	ctr *dagger.Container,
) (string, error) {
	// Should receive alpine:latest container by default
	return ctr.WithExec([]string{"cat", "/etc/alpine-release"}).Stdout(ctx)
}
