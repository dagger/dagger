package main

import (
	"context"
)

type HelloWorld struct{}

func (m *HelloWorld) Build(ctx context.Context, source *Directory, architecture string, os string) *Container {

	dir := dag.Container().
		From("golang:1.21").
		WithMountedDirectory("/src", source).
		WithWorkdir("/src").
		WithEnvVariable("GOARCH", architecture).
		WithEnvVariable("GOOS", os).
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-o", "build/"}).
		Directory("/src/build")

	return dag.Container().
		From("alpine:latest").
		WithDirectory("/usr/local/bin", dir)
}
