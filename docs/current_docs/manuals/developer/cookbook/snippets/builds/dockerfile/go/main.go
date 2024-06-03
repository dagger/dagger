package main

import (
	"context"
)

type MyModule struct{}

// Build and publish image from existing Dockerfile
func (m *MyModule) Build(
	ctx context.Context,
	// location of directory containing Dockerfile
	src *Directory,
) (string, error) {
	ref, err := dag.Container().
		WithDirectory("/src", src).
		WithWorkdir("/src").
		Directory("/src").
		DockerBuild(). // build from Dockerfile
		Publish(ctx, "ttl.sh/hello-dagger")

	if err != nil {
		return "", err
	}

	return ref, nil
}
