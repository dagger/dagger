package main

import (
	"context"
	"dagger/bar/internal/dagger"
)

type Bar struct{}

func (*Bar) Fetch(ctx context.Context, vol *dagger.CacheVolume) (string, error) {
	return dag.Container().
		From("alpine").
		WithMountedCache("/tmp-cache-mount-bar", vol).
		WithExec([]string{"sh", "-c", "cat /tmp-cache-mount-bar/input.txt"}).
		Stdout(ctx)
}
