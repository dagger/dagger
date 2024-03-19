package main

import (
	"context"
)

type MyModule struct{}

// starts and returns an HTTP service
func (m *MyModule) HttpService() *Service {
	return dag.Container().
		From("python").
		WithWorkdir("/srv").
		WithNewFile("index.html", ContainerWithNewFileOpts{
			Contents: "Hello, world!",
		}).
		WithExec([]string{"python", "-m", "http.server", "8080"}).
		WithExposedPort(8080).
		AsService()
}

// sends a request to an HTTP service and returns the response
func (m *MyModule) Get(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine").
		WithServiceBinding("www", m.HttpService()).
		WithExec([]string{"wget", "-O-", "http://www:8080"}).
		Stdout(ctx)
}
