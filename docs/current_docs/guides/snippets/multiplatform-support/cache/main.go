package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	gitRepo := client.Git("https://github.com/dagger/dagger.git").
		Branch("85dbfe92e85ba7b67f7baec98514d3bd27588a82").
		Tree()

	build := func() {
		ctr := client.Container()

		// pull the golang image for the host platform
		ctr = ctr.From("golang:1.20-alpine")

		// ensure the binary will be statically linked and thus executable
		// in the final image
		ctr = ctr.WithEnvVariable("CGO_ENABLED", "0")

		// mount in an empty dir to put the built binary
		ctr = ctr.WithDirectory("/output", client.Directory())

		// mount in our source code
		ctr = ctr.WithDirectory("/src", gitRepo)

		// mount caches for Go modules and build outputs
		ctr = ctr.WithMountedCache("/go/pkg/mod", client.CacheVolume("go-mod"))
		ctr = ctr.WithMountedCache("/root/.cache/go-build", client.CacheVolume("go-build"))

		// set GOCACHE explicitly to point to our mounted cache
		ctr = ctr.WithEnvVariable("GOCACHE", "/root/.cache/go-build")

		// build the binary and put the result at the mounted output
		// directory
		ctr = ctr.WithWorkdir("/src")
		ctr = ctr.WithExec([]string{
			"go", "build",
			"-o", "/output/dagger",
			"./cmd/dagger",
		})

		if _, err := ctr.Stdout(ctx); err != nil {
			panic(err)
		}
	}

	fmt.Println("Running first build (cache will be empty)...")
	startTime := time.Now()
	build()
	firstBuildDuration := time.Since(startTime)
	fmt.Printf("First build took %s\n", firstBuildDuration)

	fmt.Println("Running second build (cache will be used)...")
	startTime = time.Now()
	build()
	secondBuildDuration := time.Since(startTime)
	fmt.Printf("Second build took %s\n", secondBuildDuration)

	fmt.Printf("Using cache improved build time by %s\n", firstBuildDuration-secondBuildDuration)
}
