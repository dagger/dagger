package main

import (
	"context"
	"time"
)

type MyModule struct{}

// Build and publish image with oci labels
func (m *MyModule) Build(
	ctx context.Context,
) (string, error) {
	ref, err := dag.Container().
		From("alpine").
		WithLabel("org.opencontainers.image.title", "my-alpine").
		WithLabel("org.opencontainers.image.version", "1.0").
		WithLabel("org.opencontainers.image.created", time.Now().String()).
		WithLabel("org.opencontainers.image.source", "https://github.com/alpinelinux/docker-alpine").
		WithLabel("org.opencontainers.image.licenses", "MIT").
		Publish(ctx, "ttl.sh/my-alpine")

	if err != nil {
		return "", err
	}

	return ref, nil
}
