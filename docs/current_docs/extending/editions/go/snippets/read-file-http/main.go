package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) ReadFileHttp(
	ctx context.Context,
	url string,
) *dagger.Container {
	file := dag.HTTP(url)
	return dag.Container().
		From("alpine:latest").
		WithFile("/src/myfile", file)
}
