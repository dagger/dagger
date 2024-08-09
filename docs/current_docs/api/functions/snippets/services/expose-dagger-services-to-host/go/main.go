package main

import "main/internal/dagger"

type MyModule struct{}

// Start and return an HTTP service
func (m *MyModule) HttpService() *dagger.Service {
	return dag.Container().
		From("python").
		WithWorkdir("/srv").
		WithNewFile("index.html", "Hello, world!").
		WithExec([]string{"python", "-m", "http.server", "8080"}).
		WithExposedPort(8080).
		AsService()
}
