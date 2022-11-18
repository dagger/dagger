package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	if err := build(context.Background()); err != nil {
		fmt.Println(err)
	}
}

func build(ctx context.Context) error {
	fmt.Println("Building with Dagger")

	// define build matrix
	oses := []string{"linux", "darwin"}
	arches := []string{"amd64", "arm64"}
	// highlight-start
	goVersions := []string{"1.18", "1.19"}
	// highlight-end

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		return err
	}
	defer client.Close()

	// get reference to the local project
	src := client.Host().Directory(".")

	// create empty directory to put build outputs
	outputs := client.Directory()

	// highlight-start
	for _, version := range goVersions {
		// get `golang` image for specified Go version
		imageTag := fmt.Sprintf("golang:%s", version)
		golang := client.Container().From(imageTag)
		// highlight-end
		// mount cloned repository into `golang` image
		golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

		for _, goos := range oses {
			for _, goarch := range arches {
				// create a directory for each os, arch and version
				// highlight-start
				path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch)
				// highlight-end
				// set GOARCH and GOOS in the build environment
				build := golang.WithEnvVariable("GOOS", goos)
				build = build.WithEnvVariable("GOARCH", goarch)

				// build application
				build = build.Exec(dagger.ContainerExecOpts{
					Args: []string{"go", "build", "-o", path},
				})

				// get reference to build output directory in container
				outputs = outputs.WithDirectory(path, build.Directory(path))
			}
		}
		// highlight-start
	}
	// highlight-end
	// write build artifacts to host
	_, err = outputs.Export(ctx, ".")
	if err != nil {
		return err
	}
	return nil
}
