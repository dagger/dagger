// ... somewhere in your test suite setup
dockerd, err := c.Container().From("docker:dind").Service().Start(ctx)

// dockerd is now running, and will stay running
// so you don't have to worry about it restarting after a 10 second gap

// then in all of your tests, continue to use an explicit binding:
tests, err := c.Container().From("golang").
	WithServiceBinding("docker", dockerd).
	WithEnvVariable("DOCKER_HOST", "tcp://docker:2375").
	WithExec([]string{"go", "test", "./..."}).
	Sync(ctx)

// or, if you prefer
// trust `Endpoint()` to construct the address.
//
// note that this has the exact same non-cache-busting semantics as WithServiceBinding,
// since hostnames are stable and content-addressed
//
// this could be part of the global test suite setup.
dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{
	Scheme: "tcp",
})

tests, err := c.Container().From("golang").
	WithEnvVariable("DOCKER_HOST", dockerHost).
	WithExec([]string{"go", "test", "./..."}).
	Sync(ctx)

// Service.Stop() is available to explicitly stop the service if needed
