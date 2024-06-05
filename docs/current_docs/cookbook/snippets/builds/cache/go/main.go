package main

import (
	"context"
	"time"
)

type MyModule struct{}

// Run a build with cache invalidation
func (m *MyModule) Build(
	ctx context.Context,
) (string, error) {
	output, err := dag.Container().
		From("alpine").
		// comment out the line below to see the cached date output
		WithEnvVariable("CACHEBUSTER", time.Now().String()).
		WithExec([]string{"date"}).
		Stdout(ctx)

	if err != nil {
		return "", err
	}

	return output, nil
}
