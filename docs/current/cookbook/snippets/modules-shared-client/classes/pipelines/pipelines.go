package pipelines

import (
	"context"
	"dagger.io/dagger"
)

type Pipeline struct {
	client *dagger.Client
}

// Create a pipeline structure
func New(client *dagger.Client) *Pipeline {
	return &Pipeline{client: client}
}

// create base image
func (p *Pipeline) base() *dagger.Container {
	return p.client.
		Container().
		From("alpine:latest")
}

// run command in base image
func (p *Pipeline) Version(ctx context.Context) string {
	out, err := p.
		base().
		WithExec([]string{"cat", "/etc/alpine-release"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	return out
}
