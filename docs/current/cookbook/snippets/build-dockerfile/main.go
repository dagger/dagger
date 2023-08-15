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

	contextDir := client.Host().Directory("/workspace/project/")

	ref, err := contextDir.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "custom.Dockerfile",
		}).
		Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		panic(err)
	}

	fmt.Printf("Published image to :%s\n", ref)
}
