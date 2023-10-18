package main_test

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestFoo(t *testing.T) {
	ctx := context.Background()

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)

	dockerd, err := c.Container().From("docker:dind").AsService().Start(ctx)
	require.NoError(t, err)

	// dockerd is now running, and will stay running
	// so you don't have to worry about it restarting after a 10 second gap

	// then in all of your tests, continue to use an explicit binding:
	_, err = c.Container().From("golang").
		WithServiceBinding("docker", dockerd).
		WithEnvVariable("DOCKER_HOST", "tcp://docker:2375").
		WithExec([]string{"go", "test", "./..."}).
		Sync(ctx)
	require.NoError(t, err)

	// or, if you prefer
	// trust `Endpoint()` to construct the address
	//
	// note that this has the exact same non-cache-busting semantics as WithServiceBinding,
	// since hostnames are stable and content-addressed
	//
	// this could be part of the global test suite setup.
	dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "tcp",
	})
	require.NoError(t, err)

	_, err = c.Container().From("golang").
		WithEnvVariable("DOCKER_HOST", dockerHost).
		WithExec([]string{"go", "test", "./..."}).
		Sync(ctx)
	require.NoError(t, err)

	// Service.Stop() is available to explicitly stop the service if needed
}
