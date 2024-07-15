package main

import (
	"context"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Build and publish Docker container
func (m *MyModule) Build(
	ctx context.Context,
	// source code location
	// can be local directory or remote Git repository
	src *dagger.Directory,
) (string, error) {
	// build app
	builder := dag.Container().
		From("golang:latest").
		WithDirectory("/src", src).
		WithWorkdir("/src").
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-o", "myapp"})

	// publish binary on alpine base
	prodImage := dag.Container().
		From("alpine").
		WithFile("/bin/myapp", builder.File("/src/myapp")).
		WithEntrypoint([]string{"/bin/myapp"})

	// publish to ttl.sh registry
	addr, err := prodImage.Publish(ctx, "ttl.sh/myapp:latest")
	if err != nil {
		return "", err
	}

	return addr, nil
}
