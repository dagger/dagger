package main

import (
	"context"
	"fmt"
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
		WithExposedPort(8080)

	// create client container with service binding
	// access HTTP service and print result
	val, err := client.Container().
		From("alpine").
		WithServiceBinding("www", httpSrv).
		WithExec([]string{"wget", "-O-", "http://www:8080"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(val)
}
