package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

// Build and return directory of go binaries
func (m *MyModule) Build(
	ctx context.Context,
	// Source code location
	src *Directory,
) *Directory {
	// define build matrix
	gooses := []string{"linux", "darwin"}
	goarches := []string{"amd64", "arm64"}

	// create empty directory to put build artifacts
	outputs := dag.Directory()

	golang := dag.Container().
		From("golang:latest").
		WithDirectory("/src", src).
		WithWorkdir("/src")

	for _, goos := range gooses {
		for _, goarch := range goarches {
			// create directory for each OS and architecture
			path := fmt.Sprintf("build/%s/%s/", goos, goarch)

			// build artifact
			build := golang.
				WithEnvVariable("GOOS", goos).
				WithEnvVariable("GOARCH", goarch).
				WithExec([]string{"go", "build", "-o", path})

			// add build to outputs
			outputs = outputs.WithDirectory(path, build.Directory(path))
		}
	}

	// return build directory
	return outputs
}
