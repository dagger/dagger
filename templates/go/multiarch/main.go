package main

import (
	"context"
	"fmt"

	"go.dagger.io/dagger/sdk/go/dagger"
	"go.dagger.io/dagger/sdk/go/dagger/api"
)

func main() {
	err := build()
	if err != nil {
		fmt.Println(err)
	}
}

func build() error {
	ctx := context.Background()

	// Our build matrix
	architectures := []string{"amd64", "arm", "arm64", "s390x"}
	goVersions := []string{"1.18", "1.19"}

	// create a Dagger client
	client, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	// get the projects source directory
	repo := client.Core().Git("https://github.com/dagger/dagger.git")
	src, err := repo.Branch("main").Tree().ID(ctx)
	if err != nil {
		return err
	}

	// reference to the current working directory
	workdir := client.Core().Host().Workdir()

	for _, version := range goVersions {
		// Get golang image
		imageTag := fmt.Sprintf("golang:%s", version)
		golang := client.Core().Container().From(api.ContainerAddress(imageTag))

		// Mount source
		golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

		for _, arch := range architectures {
			fmt.Printf("Building %s with go %s\n", arch, version)
			outputPath := fmt.Sprintf("build/dagger_%s_%s", version, arch)

			// Set GOARCH and GOOS
			build := golang.WithEnvVariable("GOOS", "linux")
			build = build.WithEnvVariable("GOARCH", arch)

			build = build.Exec(api.ContainerExecOpts{
				Args: []string{"go", "build", "-o", outputPath, "./cmd/dagger"},
			})

			// Get build output from builder
			output, err := build.Directory("/src/build").ID(ctx)
			if err != nil {
				return err
			}

			// Write the build output to the host
			_, err = workdir.Write(ctx, output)
			if err != nil {
				return err
			}

		}
	}

	return nil
}
