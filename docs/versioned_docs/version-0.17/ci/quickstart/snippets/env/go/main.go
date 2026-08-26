package main

import "dagger/hello-dagger/internal/dagger"

type HelloDagger struct{}

// Build a ready-to-use development environment
func (m *HelloDagger) BuildEnv(
	// +defaultPath="/"
	source *dagger.Directory,
) *dagger.Container {
	// create a Dagger cache volume for dependencies
	nodeCache := dag.CacheVolume("node")
	return dag.Container().
		// start from a base Node.js container
		From("node:21-slim").
		// add the source code at /src
		WithDirectory("/src", source).
		// mount the cache volume at /root/.npm
		WithMountedCache("/root/.npm", nodeCache).
		// change the working directory to /src
		WithWorkdir("/src").
		// run npm install to install dependencies
		WithExec([]string{"npm", "install"})
}
