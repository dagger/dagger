package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

// Publish a container image to a private registry
func (m *MyModule) Publish(
	ctx context.Context,
	// Registry address
	registry string,
	// Registry username
	username string,
	// Registry password
	password *Secret,
) (string, error) {
	return dag.Container().
		From("nginx:1.23-alpine").
		WithNewFile(
            "/usr/share/nginx/html/index.html",
			"Hello from Dagger!",
            dagger.ContainerWithNewFileOpts{Permissions: 0o400},
        ).
		WithRegistryAuth(registry, username, password).
		Publish(ctx, fmt.Sprintf("%s/%s/my-nginx", registry, username))
}
