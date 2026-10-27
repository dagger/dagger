// A module written for the API before v1.0, whose check returns a container
// that fails when it runs. The check's body records each of its runs.
package main

import (
	"context"
	"crypto/rand"
	"strconv"
	"strings"

	"dagger/check-caching-legacy/internal/dagger"
)

type CheckCachingLegacy struct{}

// Returns a container which runs as a failing check
// +check
func (m *CheckCachingLegacy) FailingContainer(ctx context.Context) (*dagger.Container, error) {
	if _, err := runLog().
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "echo failing-container >> /runs/log"}).
		Sync(ctx); err != nil {
		return nil, err
	}
	return dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 1"}), nil
}

// Runs counts the recorded runs of the check's body.
// +cache="never"
func (m *CheckCachingLegacy) Runs(ctx context.Context) (int, error) {
	out, err := runLog().
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "grep -cx failing-container /runs/log || true"}).
		Stdout(ctx)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

func runLog() *dagger.Container {
	return dag.Container().
		From("alpine:3").
		WithMountedCache("/runs", dag.CacheVolume("check-caching-legacy-runs-"+nonce))
}
