package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"

	"dagger/hello-dagger/internal/dagger"
)

type HelloDagger struct{}

// Publish the application container after building and testing it on-the-fly
func (m *HelloDagger) Publish(
	ctx context.Context,
	// +defaultPath="/"
	source *dagger.Directory,
) (string, error) {
	// call Dagger Function to run unit tests
	_, err := m.Test(ctx, source)
	if err != nil {
		return "", err
	}
	// call Dagger Function to build the application image
	// publish the image to ttl.sh
	address, err := m.Build(source).
		Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		return "", err
	}
	return address, nil
}
