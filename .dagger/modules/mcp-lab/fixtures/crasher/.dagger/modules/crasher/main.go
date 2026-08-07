// A tiny workspace module for MCP crash-observability QA.
//
// Served by `dagger mcp` under the object-tools scheme, its methods become MCP
// tools alongside the builtins (ReadLogs, ListServices, ...). Each starts a
// service engineered to die in a particular way, so an MCP client can walk the
// exact "my service crashed, now what?" paths a model walks.
package main

import (
	"context"
	"fmt"
	"time"

	"dagger/crasher/internal/dagger"
)

type Crasher struct{}

// StartCrasher starts a service that becomes healthy (listening on :8080),
// then after delaySeconds prints a FATAL line to stderr and exits nonzero —
// the "crashed mid-session" scenario.
//
// +cache="never"
func (m *Crasher) StartCrasher(
	ctx context.Context,
	// +optional
	// +default=30
	delaySeconds int,
) (string, error) {
	script := fmt.Sprintf(`echo "crasher: booting"
( while true; do printf 'HTTP/1.1 200 OK\r\n\r\nok\n' | nc -l -p 8080; done ) &
echo "crasher: listening on :8080"
sleep %d
echo "crasher: FATAL: simulated crash - the cause of death is THIS log line" >&2
exit 7`, delaySeconds)
	svc, err := dag.Container().
		From("alpine:3.20").
		WithEnvVariable("CACHE_BUST", fmt.Sprintf("%d", time.Now().UnixNano())).
		WithExposedPort(8080).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{"sh", "-c", script},
		}).
		Start(ctx)
	if err != nil {
		return "", err
	}
	hostname, err := svc.Hostname(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("started crasher at http://%s:8080 - it will crash in %d seconds", hostname, delaySeconds), nil
}

// StartDoomed starts a service that dies before its healthcheck ever passes,
// so the start itself fails — the "crashed on boot" scenario.
//
// +cache="never"
func (m *Crasher) StartDoomed(ctx context.Context) (string, error) {
	svc, err := dag.Container().
		From("alpine:3.20").
		WithEnvVariable("CACHE_BUST", fmt.Sprintf("%d", time.Now().UnixNano())).
		WithExposedPort(8080).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{"sh", "-c",
				`echo "doomed: FATAL: dying before the healthcheck - THIS line is the diagnosis" >&2; exit 42`},
		}).
		Start(ctx)
	if err != nil {
		return "", err
	}
	hostname, err := svc.Hostname(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("unexpectedly started doomed service at %s", hostname), nil
}

// Poke fetches a URL from inside the session's network, to check whether a
// service is still answering.
//
// +cache="never"
func (m *Crasher) Poke(ctx context.Context, url string) (string, error) {
	return dag.Container().
		From("alpine:3.20").
		WithEnvVariable("CACHE_BUST", fmt.Sprintf("%d", time.Now().UnixNano())).
		WithExec(
			[]string{"sh", "-c", fmt.Sprintf(`wget -q -O - -T 5 %q || echo "poke failed: exit $?"`, url)},
			dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny},
		).
		Stdout(ctx)
}
