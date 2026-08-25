package main

import (
	"context"
	"fmt"

	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

// Tag a container image multiple times and publish it to a private registry
func (m *MyModule) Publish(
	ctx context.Context,
	// Registry address
	registry string,
	// Registry username
	username string,
	// Registry password
	password *dagger.Secret,
) ([]string, error) {
	tags := [4]string{"latest", "1.0-alpine", "1.0", "1.0.0"}
	addr := []string{}
	ctr := dag.Container().
		From("nginx:1.23-alpine").
		WithNewFile(
			"/usr/share/nginx/html/index.html",
			"Hello from Dagger!",
			dagger.ContainerWithNewFileOpts{Permissions: 0o400},
		).
		WithRegistryAuth(registry, username, password)

	for _, tag := range tags {
		a, err := ctr.Publish(ctx, fmt.Sprintf("%s/%s/my-nginx:%s", registry, username, tag))
		if err != nil {
			return addr, err
		}
		addr = append(addr, a)
	}
	return addr, nil
}
