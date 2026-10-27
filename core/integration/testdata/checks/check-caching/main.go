// A module whose checks record each run of their bodies, so tests can count
// how often a check really ran.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"strings"

	"dagger/check-caching/internal/dagger"
)

type CheckCaching struct{}

// A check that passes
// +check
func (m *CheckCaching) Pass(ctx context.Context) error {
	return record(ctx, "pass")
}

// A check that fails
// +check
func (m *CheckCaching) Fail(ctx context.Context) error {
	if err := record(ctx, "fail"); err != nil {
		return err
	}
	return errors.New("this check always fails")
}

// A check cached for one session
// +check
// +cache="session"
func (m *CheckCaching) PerSession(ctx context.Context) error {
	return record(ctx, "per-session")
}

// A check that is never cached
// +check
// +cache="never"
func (m *CheckCaching) Never(ctx context.Context) error {
	return record(ctx, "never")
}

// Runs counts the recorded runs of a check's body.
// +cache="never"
func (m *CheckCaching) Runs(ctx context.Context, name string) (int, error) {
	out, err := runLog().
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "grep -cx '" + name + "' /runs/log || true"}).
		Stdout(ctx)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

func runLog() *dagger.Container {
	return dag.Container().
		From("alpine:3").
		WithMountedCache("/runs", dag.CacheVolume("check-caching-runs-"+nonce))
}

// record appends name to the run log. Its exec is keyed by a fresh random
// value, so each call adds exactly one line.
func record(ctx context.Context, name string) error {
	_, err := runLog().
		WithEnvVariable("NONCE", rand.Text()).
		WithExec([]string{"sh", "-c", "echo '" + name + "' >> /runs/log"}).
		Sync(ctx)
	return err
}
