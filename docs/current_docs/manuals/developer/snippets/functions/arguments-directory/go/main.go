package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Tree(ctx context.Context, src *Directory, depth string) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/mnt", src).
		WithWorkdir("/mnt").
		WithExec([]string{"apk", "add", "tree"}).
		WithExec([]string{"tree", "-L", depth}).
		Stdout(ctx)
}
