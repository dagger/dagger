package main

import (
	"context"
	"dagger/tests/internal/dagger"
	"errors"

	"github.com/sourcegraph/conc/pool"
)

type Tests struct{}

func (m *Tests) All(ctx context.Context) error {
	p := pool.New().WithErrors().WithContext(ctx)

	p.Go(m.Hello)
	p.Go(m.CustomGreeting)

	return p.Wait()
}

func (m *Tests) All_Manual(ctx context.Context) error {
	var err error

	err = m.Hello(ctx)
	if err != nil {
		return err
	}

	err = m.CustomGreeting(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (m *Tests) Hello(ctx context.Context) error {
	greeting, err := dag.Greeter().Hello(ctx, "World")
	if err != nil {
		return err
	}

	if greeting != "Hello, World!" {
		return errors.New("unexpected greeting")
	}

	return nil
}

func (m *Tests) CustomGreeting(ctx context.Context) error {
	greeting, err := dag.Greeter(dagger.GreeterOpts{
		Greeting: "Welcome",
	}).Hello(ctx, "World")
	if err != nil {
		return err
	}

	if greeting != "Welcome, World!" {
		return errors.New("unexpected greeting")
	}

	return nil
}
