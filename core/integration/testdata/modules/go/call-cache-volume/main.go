package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Cacher(ctx context.Context, cache *dagger.CacheVolume, val string) (string, error) {
	return dag.Container().
		From("alpine:3.22.1").
		WithMountedCache("/cache", cache).
		WithExec([]string{"sh", "-c", "echo $0 >> /cache/vals", val}).
		WithExec([]string{"cat", "/cache/vals"}).
		Stdout(ctx)
}
