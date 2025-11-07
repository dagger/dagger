// A module for HelloWithChecks functions
package main

import (
	"context"

	"dagger/hello-with-checks/internal/dagger"
)

type HelloWithChecks struct{}

// Returns a passing check
// +check
func (m *HelloWithChecks) PassingCheck(ctx context.Context) error {
	_, err := dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 0"}).Sync(ctx)
	return err
}

// Returns a failing check
// +check
func (m *HelloWithChecks) FailingCheck(ctx context.Context) error {
	_, err := dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 1"}).Sync(ctx)
	return err
}

// Returns a container which runs as a passing check
// +check
func (m *HelloWithChecks) PassingContainer() *dagger.Container {
	return dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 0"})
}

// Returns a container which runs as a failing check
// +check
func (m *HelloWithChecks) FailingContainer() *dagger.Container {
	return dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 1"})
}
