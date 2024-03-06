package main

import (
	"context"

	"dagger.io/dagger/dag"
)

type MyModule struct{}

func (m *MyModule) HttpService() *Service {
	return dag.Container().
		From("python").
		WithDirectory("/srv", dag.Directory().WithNewFile("index.html", "Hello, world!")).
		WithWorkdir("/srv").
		WithExec([]string{"python", "-m", "http.server", "8080"}).
		WithExposedPort(8080).
		AsService()
}

func (m *MyModule) Get(ctx context.Context) string {
	val, err := dag.Container().
		From("alpine").
		WithServiceBinding("www", m.HttpService()).
		WithExec([]string{"wget", "-O-", "http://www:8080"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}
	return val
}
