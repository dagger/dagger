package main

import (
	"context"
	"dagger/foo/internal/dagger"
	"fmt"
)

type Foo struct{}

func (*Foo) Populate(ctx context.Context, input string) (*dagger.Container, error) {
	return dag.Container().
		From("alpine").
		WithMountedCache("/tmp-cache-mount", dag.CacheVolume("cache-name")).
		WithExec([]string{"sh", "-c", fmt.Sprintf("echo '%s' > /tmp-cache-mount/input.txt", input)}).
		Sync(ctx)
}

func (*Foo) Fetch(ctx context.Context) (string, error) {
	cache := dag.CacheVolume("cache-name")
	return dag.Bar().Fetch(ctx, cache)
}
