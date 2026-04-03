// A module where one service starts fine and another fails immediately.
// Used to test that partial startup failure cancels sibling services
// and surfaces the error instead of hanging forever.
package main

import (
	"dagger/partial-failure/internal/dagger"
)

type PartialFailure struct{}

// A healthy service that starts normally.
// +up
func (m *PartialFailure) Healthy() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithExposedPort(8080).
		AsService()
}

// A broken service that fails immediately on startup.
// +up
func (m *PartialFailure) Broken() *dagger.Service {
	return dag.Container().
		From("alpine:3").
		WithExec([]string{"sh", "-c", "echo 'startup failed' >&2; exit 1"}).
		WithExposedPort(9999).
		AsService()
}
