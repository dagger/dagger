package main

import "dagger/hello-dagger/internal/dagger"

type HelloDagger struct{}

// Build the application container
func (m *HelloDagger) Build(
	// +defaultPath="/"
	source *dagger.Directory,
) *dagger.Container {
	// get the build environment container
	// by calling another Dagger Function
	build := m.BuildEnv(source).
		// build the application
		WithExec([]string{"npm", "run", "build"}).
		// get the build output directory
		Directory("./dist")
	// start from a slim NGINX container
	return dag.Container().From("nginx:1.25-alpine").
		// copy the build output directory to the container
		WithDirectory("/usr/share/nginx/html", build).
		// expose the container port
		WithExposedPort(80)
}
