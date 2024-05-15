package main

type HelloDagger struct{}

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
