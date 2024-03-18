package main

import (
	"context"
)

type MyModule struct{}

func (m *MyModule) HttpService(ctx context.Context) (string, error) {
	// create HTTP service container with exposed port 8080
	httpSrv := dag.Container().
		From("python").
		WithWorkdir("/srv").
		WithNewFile("index.html", ContainerWithNewFileOpts{
			Contents: "Hello, world!",
		}).
		WithExec([]string{"python", "-m", "http.server", "8080"}).
		WithExposedPort(8080).
		AsService()

	// get endpoint
	val, err := httpSrv.Endpoint(ctx)
	if err != nil {
		panic(err)
	}

	if err != nil {
		return "", err
	}
	return val, nil
}
