package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) ReadFile(ctx context.Context, source *dagger.File) (string, error) {
	contents, err := dag.Container().
		From("alpine:latest").
		WithFile("/tmp/myfile", source).
		WithExec([]string{"cat", "/tmp/myfile"}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	return contents, nil
}
