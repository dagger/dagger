package main

import (
	"context"
)

type MyModule struct{}

// Build multi stage docker container and publish to registry
func (m *MyModule) Build(
	ctx context.Context,
	// source code location
	// can be local directory or remote Git repository
	dir *Directory,
) string {
	// build app
	builder := dag.Container().
		From("golang:latest").
		WithDirectory("/src", dir).
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
		panic(err)
	}

	return addr
}
