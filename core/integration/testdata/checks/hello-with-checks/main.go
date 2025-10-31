// A module for HelloWithChecks functions
package main

import (
	"context"
	"dagger/hello-with-checks/internal/dagger"
)

type HelloWithChecks struct{}

// Returns a passing check
func (m *HelloWithChecks) PassingCheck(ctx context.Context) (dagger.CheckStatus, error) {
	_, err := dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 0"}).Sync(ctx)
	return dagger.CheckStatusCompleted, err
}

// Returns a failing check
func (m *HelloWithChecks) FailingCheck(ctx context.Context) (dagger.CheckStatus, error) {
	_, err := dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 1"}).Sync(ctx)
	return dagger.CheckStatusCompleted, err
}
