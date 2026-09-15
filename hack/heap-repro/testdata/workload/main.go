// The workload module for hack/heap-repro. Each call runs in a fresh module
// runtime, which connects to the engine as a nested client, so calling it with
// a new seed every time exercises one client lifecycle per call.
package main

import "context"

type Workload struct{}

// Hello runs a short container exec whose cache key depends on seed, so every
// distinct seed costs one fresh module runtime and one fresh exec.
func (m *Workload) Hello(ctx context.Context, seed string) (string, error) {
	return dag.Container().
		From("alpine:3.20").
		WithEnvVariable("SEED", seed).
		WithExec([]string{"sh", "-c", "echo hello from $SEED; echo working; echo done"}).
		Stdout(ctx)
}
