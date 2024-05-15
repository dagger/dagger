package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, src *Directory, arch string, os string) *Container {

	dir := dag.Container().
		From("golang:1.21").
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithEnvVariable("GOARCH", arch).
		WithEnvVariable("GOOS", os).
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-o", "build/"}).
		Directory("/src/build")

	return dag.Container().
		From("alpine:latest").
		WithDirectory("/usr/local/bin", dir)
}
