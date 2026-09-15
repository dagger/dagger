package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Tree(ctx context.Context, src *dagger.Directory, depth string) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/mnt", src).
		WithWorkdir("/mnt").
		WithExec([]string{"apk", "add", "tree"}).
		WithExec([]string{"tree", "-L", depth}).
		Stdout(ctx)
}
