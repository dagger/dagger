package main

import "dagger/my-module/internal/dagger"

type MyModule struct{}

// Build an application using cached dependencies
func (m *MyModule) Build(
	// Source code location
	source *dagger.Directory,
) *dagger.Container {
	return dag.Container().
		From("golang:1.21").
		WithDirectory("/src", source).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod-121")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build-121")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithExec([]string{"go", "build"})
}
