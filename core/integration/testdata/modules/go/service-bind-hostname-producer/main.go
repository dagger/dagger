package main

import (
	"context"

	"dagger/producer/internal/dagger"
)

type Producer struct{}

// Registry models the shape a real module uses to hand a service to its
// caller: a module object carrying the *dagger.Service on a declared field,
// alongside the hostname that service advertises itself under.
type Registry struct {
	Svc      *dagger.Service
	Hostname string
}

func (m *Producer) registry(hostname, greeting string) *Registry {
	svc := dag.Container().
		From("busybox:1.37.0").
		WithWorkdir("/srv").
		WithNewFile("index.html", greeting).
		WithDefaultArgs([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname(hostname)

	return &Registry{Svc: svc, Hostname: hostname}
}

// Serve returns a registry whose service this module has already started.
// Starting module-side is what makes the service's custom hostname resolvable
// only within this module's DNS domain, so the caller's later binding refers to
// a running service it cannot reach through its own search domains.
func (m *Producer) Serve(ctx context.Context) (*Registry, error) {
	reg := m.registry("started-registry-host", "hello from the started producer")
	if _, err := reg.Svc.Start(ctx); err != nil {
		return nil, err
	}
	return reg, nil
}

// ServeLazy returns a registry without starting anything, so the caller's
// binding is what starts the service. That ordering worked before the fix, and
// is covered here to keep it working.
func (m *Producer) ServeLazy() *Registry {
	return m.registry("lazy-registry-host", "hello from the lazy producer")
}
