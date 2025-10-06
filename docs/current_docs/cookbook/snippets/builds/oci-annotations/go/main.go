package main

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
)

type MyModule struct{}

// Build and publish image with OCI annotations
func (m *MyModule) Build(ctx context.Context) (string, error) {
	address, err := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/src").
		WithExec([]string{"git", "clone", "https://github.com/dagger/dagger", "."}).
		WithAnnotation("org.opencontainers.image.authors", "John Doe").
		WithAnnotation("org.opencontainers.image.title", "Dagger source image viewer").
		Publish(ctx, fmt.Sprintf("ttl.sh/custom-image-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		return "", err
	}
	return address, nil
}
