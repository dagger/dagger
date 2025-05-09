package main

import (
	"context"
	"dagger/my-module/internal/dagger"
	"strings"
)

type MyModule struct{}

func (m *MyModule) Load(ctx context.Context, docker *dagger.Socket, tag string) (*dagger.Container, error) {
	// Create a new container
	ctr := dag.Container().
		From("alpine").
		WithExec([]string{"apk", "add", "git"})

	// Create a new container from the docker CLI image
	// Mount the Docker socket from the host
	// Mount the newly-built container as a tarball
	dockerCli := dag.Container().
		From("docker:cli").
		WithUnixSocket("/var/run/docker.sock", docker).
		WithMountedFile("image.tar", ctr.AsTarball())

	// Load the image from the tarball
	out, err := dockerCli.
		WithExec([]string{"docker", "load", "-i", "image.tar"}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}

	// Add the tag to the image
	image := strings.TrimSpace(strings.SplitN(out, ":", 2)[1])
	return dockerCli.
		WithExec([]string{"docker", "tag", image, tag}).
		Sync(ctx)
}
