package main

import (
	"context"

	"golang.org/x/sync/errgroup"
)

type MyModule struct{}

func (m *MyModule) Foo(ctx context.Context) error {
	// clone the source code repository
	source := dag.
		Git("https://github.com/dagger/hello-dagger").
		Branch("main").Tree()

	// set up a container with the source code mounted
	// install dependencies
	container := dag.Container().
		From("node:latest").
		WithDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"})

	// run operations concurrently
	// emit a span for each
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		_, span := Tracer().Start(ctx, "lint code")
		defer span.End()

		_, err := container.WithExec([]string{"npm", "run", "lint"}).Sync(ctx)
		return err
	})

	eg.Go(func() error {
		_, span := Tracer().Start(ctx, "check types")
		defer span.End()

		_, err := container.WithExec([]string{"npm", "run", "type-check"}).Sync(ctx)
		return err
	})

	eg.Go(func() error {
		_, span := Tracer().Start(ctx, "run unit tests")
		defer span.End()

		_, err := container.WithExec([]string{"npm", "run", "test:unit", "run"}).Sync(ctx)
		return err
	})

	return eg.Wait()
}
