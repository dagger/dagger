package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
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

	// create a cache volume
	nodeCache := client.CacheVolume("node")

	// use a node:16-slim container
	// mount the source code directory on the host
	// at /src in the container
	// mount the cache volume to persist dependencies
	source := client.Container().
		From("node:16-slim").
		WithDirectory("/src", client.Host().Directory("."), dagger.ContainerWithDirectoryOpts{
			Exclude: []string{"node_modules/", "ci/"},
		}).
		WithMountedCache("/src/node_modules", nodeCache)

		// set the working directory in the container
		// install application dependencies
	runner := source.WithWorkdir("/src").
		WithExec([]string{"npm", "install"})

		// run application tests
	test := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"})

	// first stage
	// build application
	buildDir := test.WithExec([]string{"npm", "run", "build"}).
		Directory("./build")

		// second stage
		// use an nginx:alpine container
		// copy the build/ directory from the first stage
		// publish the resulting container to a registry
	ref, err := client.Container().
		From("nginx:1.23-alpine").
		WithDirectory("/usr/share/nginx/html", buildDir).
		Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		panic(err)
	}

	fmt.Printf("Published image to: %s\n", ref)
}
