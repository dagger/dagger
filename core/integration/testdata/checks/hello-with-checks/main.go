// A module for HelloWithChecks functions
package main

import (
	"context"
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
