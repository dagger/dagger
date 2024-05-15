package main

import "context"

type HelloDagger struct{}

// Returns a container with the production build and an NGINX service
func (m *HelloDagger) Build(source *Directory) *Container {
	// perform a multi-stage build
	// stage 1
	// use the build environment container
	// build the application
	// return the build output directory
	build := m.BuildEnv(source).
		WithExec([]string{"npm", "run", "build"}).
		Directory("./dist")
	// stage 2
	// start from a base nginx container
	// copy the build output directory to it
	// expose container port 8080
	return dag.Container().From("nginx:1.25-alpine").
		WithDirectory("/usr/share/nginx/html", build).
		WithExposedPort(80)
}

// Returns the result of running unit tests
func (m *HelloDagger) Test(ctx context.Context, source *Directory) (string, error) {
	// use the build environment container
	// run unit tests
	return m.BuildEnv(source).
		WithExec([]string{"npm", "run", "test:unit", "run"}).
		Stdout(ctx)
}

// Returns a container with the build environment
func (m *HelloDagger) BuildEnv(source *Directory) *Container {
	// create a Dagger cache volume for dependencies
	nodeCache := dag.CacheVolume("node")
	// create the build environment
	// start from a base node container
	// add source code
	// install dependencies
	return dag.Container().
		From("node:21-slim").
		WithDirectory("/src", source.WithoutDirectory("dagger")).
		WithMountedCache("/src/node_modules", nodeCache).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"})
}
