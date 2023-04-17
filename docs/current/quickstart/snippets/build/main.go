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
	test := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"})

	// build application
	// write the build output to the host
	buildDir := test.WithExec([]string{"npm", "run", "build"}).
		Directory("./build")

	_, err = buildDir.Export(ctx, "./build")
	if err != nil {
		panic(err)
	}

	e, err := buildDir.Entries(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("build dir contents:\n %s\n", e)
}
