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

	// list versions to test against
	versions := []string{"20", "22", "23"}

	// define errorgroup
	eg, ctx := errgroup.WithContext(ctx)

	// run tests concurrently
	// emit a span for each
	for _, version := range versions {
		eg.Go(func() error {
			_, span := Tracer().Start(ctx, "running unit tests with Node "+version)
			defer span.End()
			_, err := dag.Container().
				From("node:"+version).
				WithDirectory("/src", source).
				WithWorkdir("/src").
				WithExec([]string{"npm", "install"}).
				WithExec([]string{"npm", "run", "test:unit", "run"}).Sync(ctx)
			return err
		})
	}

	return eg.Wait()
}
