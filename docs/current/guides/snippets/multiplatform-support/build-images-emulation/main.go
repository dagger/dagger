package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

// the platforms to build for and push in a multi-platform image
var platforms = []dagger.Platform{
	"linux/amd64", // a.k.a. x86_64
	"linux/arm64", // a.k.a. aarch64
	"linux/s390x", // a.k.a. IBM S/390
}

// the container registry for the multi-platform image
const imageRepo = "localhost/testrepo:latest"

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// the git repository containing code for the binary to be built
	gitRepo := client.Git("https://github.com/dagger/dagger.git").
		Branch("086862926433e19e1f24cd709e6165c36bdb2633").
		Tree()

	platformVariants := make([]*dagger.Container, 0, len(platforms))
	for _, platform := range platforms {
		// pull the golang image for this platform
		ctr := client.Container(dagger.ContainerOpts{Platform: platform})
		ctr = ctr.From("golang:1.19-alpine")

		// mount in source code
		ctr = ctr.WithMountedDirectory("/src", gitRepo)

		// mount in an empty dir where the built binary will live
		ctr = ctr.WithMountedDirectory("/output", client.Directory())

		// ensure the binary will be statically linked and thus executable
		// in the final image
		ctr = ctr.WithEnvVariable("CGO_ENABLED", "0")

		// build the binary and put the result at the mounted output
		// directory
		ctr = ctr.WithWorkdir("/src")
		ctr = ctr.WithExec([]string{
			"go", "build",
			"-o", "/output/dagger",
			"/src/cmd/dagger",
		})

		// select the output directory
		outputDir := ctr.Directory("/output")

		// wrap the output directory in a new empty container marked
		// with the same platform
		binaryCtr := client.
			Container(dagger.ContainerOpts{Platform: platform}).
			WithRootfs(outputDir)
		platformVariants = append(platformVariants, binaryCtr)
	}

	// publishing the final image uses the same API as single-platform
	// images, but now additionally specify the `PlatformVariants`
	// option with the containers built before.
	imageDigest, err := client.
		Container().
		Publish(ctx, imageRepo, dagger.ContainerPublishOpts{
			PlatformVariants: platformVariants,
		})
	if err != nil {
		panic(err)
	}
	fmt.Println("Pushed multi-platform image w/ digest: ", imageDigest)
}
