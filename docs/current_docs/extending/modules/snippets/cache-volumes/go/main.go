package main

import "dagger/my-module/internal/dagger"

type MyModule struct{}

// Build an application using cached dependencies
func (m *MyModule) Build(
	// Source code location
	source *dagger.Directory,
) *dagger.Container {
	return dag.Container().
		From("node:21").
		WithDirectory("/src", source).
		WithWorkdir("/src").
		WithMountedCache("/root/.npm", dag.CacheVolume("node-21")).
		WithExec([]string{"npm", "install"})
}
