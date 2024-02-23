package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Tree(ctx context.Context, dir *Directory) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithMountedDirectory("/mnt", dir).
		WithWorkdir("/mnt").
		WithExec([]string{"tree"}).
		Stdout(ctx)
}
