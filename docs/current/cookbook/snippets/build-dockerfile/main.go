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
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// get build context directory
	contextDir := client.Host().Directory("/projects/myapp")

	// get Dockerfile in different filesystem location
	dockerfilePath := "/data/myapp/custom.Dockerfile"
	dockerfile := client.Host().File(dockerfilePath)

	// add Dockerfile to build context directory
	workspace := contextDir.WithFile("custom.Dockerfile", dockerfile)

	// build using Dockerfile
	// publish the resulting container to a registry
	ref, err := client.
		Container().
		Build(workspace, dagger.ContainerBuildOpts{
			Dockerfile: "custom.Dockerfile",
		}).
		Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		panic(err)
	}

	fmt.Printf("Published image to :%s\n", ref)
}
