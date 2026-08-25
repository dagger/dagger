package main

import (
	"context"

	"dagger/my-module/internal/telemetry"

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
	eg := new(errgroup.Group)

	// run tests concurrently
	// emit a span for each
	for _, version := range versions {
		eg.Go(func() (rerr error) {
			ctx, span := Tracer().Start(ctx, "running unit tests with Node "+version)
			defer telemetry.End(span, func() error { return rerr })
			_, err := dag.Container().
				From("node:"+version).
				WithDirectory("/src", source).
				WithWorkdir("/src").
				WithExec([]string{"npm", "install"}).
				WithExec([]string{"npm", "run", "test:unit", "run"}).
				Sync(ctx)
			return err
		})
	}

	return eg.Wait()
}
