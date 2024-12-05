package main

import (
	"dagger/my-module/internal/dagger"

	"dagger.io/dagger/dag"
)

type MyModule struct{}

func (m *MyModule) HttpService() *dagger.Service {
	return dag.Container().
		From("python").
		WithWorkdir("/srv").
		WithNewFile("index.html", "Hello, world!").
		WithDefaultArgs([]string{"python", "-m", "http.server", "8080"}).
		WithExposedPort(8080).
		AsService()
}
