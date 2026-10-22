package main

import (
	"context"

	"dagger/test/internal/dagger"
)

func New(
	ctx context.Context,
	// Unique per test run, so a handshake file never leaks between runs.
	nonce string,
) *Test {
	// The dependency and the terminal share a cache volume. The dependency
	// crashes only once the terminal creates /handshake/crash, and creates
	// /handshake/crashed just before it exits.
	handshake := dag.CacheVolume("terminal-bound-service-crash-" + nonce)

	dep := dag.Container().
		From("alpine:3.22.1").
		WithMountedCache("/handshake", handshake).
		WithDefaultArgs([]string{"sh", "-c", "until [ -e /handshake/crash ]; do sleep 0.1; done; echo dependency crashed >&2; touch /handshake/crashed; exit 42"}).
		AsService()

	return &Test{
		Ctr: dag.Container().
			From("alpine:3.22.1").
			WithMountedCache("/handshake", handshake).
			WithWorkdir("/coolworkdir").
			WithServiceBinding("dep", dep),
	}
}

type Test struct {
	Ctr *dagger.Container
}
