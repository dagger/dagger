package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// use a node:16-slim container
	// mount the source code directory on the host
	// at /src in the container
	source := client.Container().
		From("node:16-slim").
		WithDirectory("/src", client.Host().Directory("."), dagger.ContainerWithDirectoryOpts{
			Exclude: []string{"node_modules/", "ci/"},
		})

		// set the working directory in the container
		// install application dependencies
	runner := source.WithWorkdir("/src").
		WithExec([]string{"npm", "install"})

		// run application tests
	out, err := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"}).
		Stderr(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
}
