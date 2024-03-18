package main

import (
	"context"
	"io"
	"net/http"
)

type MyModule struct{}

// starts and returns an HTTP service, then exposes it to the host
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

	// expose HTTP service to host
	// FIXME
	tunnel, err := dag.Host().Tunnel(httpSrv).Start(ctx)
	if err != nil {
		return "", err
	}
	defer tunnel.Stop(ctx)

	// get HTTP service address
	srvAddr, err := tunnel.Endpoint(ctx)
	if err != nil {
		return "", err
	}

	// access HTTP service from host
	res, err := http.Get("http://" + srvAddr)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	// print response
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
