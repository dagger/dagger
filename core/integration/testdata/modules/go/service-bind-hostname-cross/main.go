package main

import (
	"context"
	"fmt"
	"time"

	"dagger/consumer/internal/dagger"
)

type Consumer struct{}

// Run binds a service the producer module already started, and reaches it by
// the hostname that module advertised. The service's custom hostname is scoped
// to the producer's DNS domain, which this module never searches — binding it
// explicitly has to work anyway.
func (m *Consumer) Run(ctx context.Context) error {
	reg := dag.Producer().Serve()

	hostname, err := reg.Hostname(ctx)
	if err != nil {
		return err
	}

	return m.get(ctx, hostname, reg.Svc(), "hello from the started producer")
}

// RunLazy binds a service nothing has started yet, so this module's own binding
// starts it.
func (m *Consumer) RunLazy(ctx context.Context) error {
	reg := dag.Producer().ServeLazy()

	hostname, err := reg.Hostname(ctx)
	if err != nil {
		return err
	}

	return m.get(ctx, hostname, reg.Svc(), "hello from the lazy producer")
}

func (m *Consumer) get(ctx context.Context, hostname string, svc *dagger.Service, want string) error {
	resp, err := dag.Container().
		From("busybox:1.37.0").
		WithEnvVariable("NOW", time.Now().String()).
		WithServiceBinding(hostname, svc).
		WithExec([]string{"wget", "-O-", "http://" + hostname}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to reach bound service: %w", err)
	}
	if resp != want {
		return fmt.Errorf("unexpected response: %q", resp)
	}
	return nil
}
