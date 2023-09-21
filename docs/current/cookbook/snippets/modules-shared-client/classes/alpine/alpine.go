package alpine

import (
	"context"
	"dagger.io/dagger"
)

type Alpine struct {
	client *dagger.Client
}

// Create a Alpine structure
func New(client *dagger.Client) *Alpine {
	return &Alpine{client: client}
}

// create base image
func (a *Alpine) base() *dagger.Container {
	return a.client.
		Container().
		From("alpine:latest")
}

// run command in base image
func (a *Alpine) Version(ctx context.Context) string {
	out, err := a.
		base().
		WithExec([]string{"cat", "/etc/alpine-release"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	return out
}
