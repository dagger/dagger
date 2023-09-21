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
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// use a golang:1.21 container
	// mount the source code directory on the host
	// at /src in the container
	// mount the cache volume to persist dependencies
	source := client.Container().
		From("golang:1.21").
		WithDirectory("/src", client.Host().Directory(".")).
		WithMountedCache("/go/pkg/mod", client.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", client.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")

	// set the working directory in the container
	// install application dependencies
	runner, err := source.WithWorkdir("/src").
		WithExec([]string{"go", "build"}).
		Sync(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(runner.ID(ctx))
}
