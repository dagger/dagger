package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Build and publish image from Dockerfile using a build context directory
// in a different location than the current working directory
func (m *MyModule) Build(
	ctx context.Context,
	// location of source directory
	src *dagger.Directory,
	// location of Dockerfile
	dockerfile *dagger.File,
) (string, error) {

	// get build context with dockerfile added
	workspace := dag.Container().
		WithDirectory("/src", src).
		WithWorkdir("/src").
		WithFile("/src/custom.Dockerfile", dockerfile).
		Directory("/src")

	// build using Dockerfile and publish to registry
	ref, err := workspace.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Dockerfile: "custom.Dockerfile",
	}).Publish(ctx, "ttl.sh/hello-dagger")

	if err != nil {
		return "", err
	}

	return ref, nil
}
