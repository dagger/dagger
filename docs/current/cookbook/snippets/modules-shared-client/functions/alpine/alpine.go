package alpine

import (
	"context"

	"dagger.io/dagger"
)

// create base image
func base(client *dagger.Client) *dagger.Container {
	return client.
		Container().
		From("alpine:latest")
}

// run command in base image
func Version(client *dagger.Client) string {
	ctx := context.Background()

	out, err := base(client).
		WithExec([]string{"cat", "/etc/alpine-release"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	return out
}
