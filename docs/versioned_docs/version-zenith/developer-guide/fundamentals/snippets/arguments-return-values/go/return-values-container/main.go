package main

import (
	"context"
	"runtime"
)

type HelloWorld struct{}

func (m *HelloWorld) Build(
	ctx context.Context,
	source *Directory,
	// +optional
	architecture string,
	// +optional
	os string,
) *Container {

	if architecture == "" {
		architecture = runtime.GOARCH
	}

	if os == "" {
		os = runtime.GOOS
	}

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
