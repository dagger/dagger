package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Build and publish image from existing Dockerfile
func (m *MyModule) Build(
	ctx context.Context,
	// location of directory containing Dockerfile
	src *dagger.Directory,
) (string, error) {
	ref, err := src.
		DockerBuild(). // build from Dockerfile
		Publish(ctx, "ttl.sh/hello-dagger")

	if err != nil {
		return "", err
	}

	return ref, nil
}
