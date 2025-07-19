package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) Build(
	ctx context.Context,
	src *dagger.Directory,
	arch string,
	os string,
) *dagger.Container {
	return dag.Container().
		From("golang:1.21").
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithEnvVariable("GOARCH", arch).
		WithEnvVariable("GOOS", os).
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-o", "build/"})
}
