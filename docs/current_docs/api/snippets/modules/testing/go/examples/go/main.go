package main

import (
	"context"
	"dagger/examples/internal/dagger"

	"github.com/sourcegraph/conc/pool"
)

type Examples struct{}

func (m *Examples) All(ctx context.Context) error {
	p := pool.New().WithErrors().WithContext(ctx)

	p.Go(m.GreeterHello)
	p.Go(m.Greeter_CustomGreeting)

	return p.Wait()
}

func (m *Examples) GreeterHello(ctx context.Context) error {
	greeting, err := dag.Greeter().Hello(ctx, "World")
	if err != nil {
		return err
	}

	// Do something with the greeting
	_ = greeting

	return nil
}

func (m *Examples) Greeter_CustomGreeting(ctx context.Context) error {
	greeting, err := dag.Greeter(dagger.GreeterOpts{
		Greeting: "Welcome",
	}).Hello(ctx, "World")
	if err != nil {
		return err
	}

	// Do something with the greeting
	_ = greeting

	return nil
}
