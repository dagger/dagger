package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))

	if err != nil {
		panic(err)
	}
	defer client.Close()

	// create HTTP service container with exposed port 8080
	httpSrv := client.Container().
		From("python").
		WithDirectory("/srv", client.Directory().WithNewFile("index.html", "Hello, world!")).
		WithWorkdir("/srv").
		WithExec([]string{"python", "-m", "http.server", "8080"}).
		WithExposedPort(8080).
		AsService()

	// expose HTTP service to host
	tunnel, err := client.Host().Tunnel(httpSrv).Start(ctx)
	if err != nil {
		panic(err)
	}
	defer tunnel.Stop(ctx)

	// get HTTP service address
	srvAddr, err := tunnel.Endpoint(ctx)
	if err != nil {
		panic(err)
	}

	// access HTTP service from host
	res, err := http.Get("http://" + srvAddr)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(body))
}
