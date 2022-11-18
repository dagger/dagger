package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
	platformFormat "github.com/containerd/containerd/platforms"
)

var platforms = []dagger.Platform{
	"linux/amd64", // a.k.a. x86_64
	"linux/arm64", // a.k.a. aarch64
	"linux/s390x", // a.k.a. IBM S/390
}

// the container registry for the multi-platform image
const imageRepo = "localhost/testrepo:latest"

// highlight-start
// util that returns the architecture of the provided platform
func architectureOf(platform dagger.Platform) string {
	return platformFormat.MustParse(string(platform)).Architecture
}

// highlight-end

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	gitRepo := client.Git("https://github.com/dagger/dagger.git").
		Branch("086862926433e19e1f24cd709e6165c36bdb2633").
		Tree()

	platformVariants := make([]*dagger.Container, 0, len(platforms))
	for _, platform := range platforms {
		// highlight-start
		// pull the golang image for the *host platform*. This is
		// accomplished by just not specifying a platform; the default
		// is that of the host.
		ctr := client.Container()
		ctr = ctr.From("golang:1.19-alpine")
		// highlight-end

		// mount in our source code
		ctr = ctr.WithMountedDirectory("/src", gitRepo)

		// mount in an empty dir to put the built binary
		ctr = ctr.WithMountedDirectory("/output", client.Directory())

		// ensure the binary will be statically linked and thus executable
		// in the final image
		ctr = ctr.WithEnvVariable("CGO_ENABLED", "0")

		// highlight-start
		// configure the go compiler to use cross-compilation targeting the
		// desired platform
		ctr = ctr.WithEnvVariable("GOOS", "linux")
		ctr = ctr.WithEnvVariable("GOARCH", architectureOf(platform))
		// highlight-end

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
		// with the platform
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
	fmt.Println("published multi-platform image with digest", imageDigest)
}
