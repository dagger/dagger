package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	repo := ""
	if len(os.Args) < 2 {
		repo = "https://github.com/goreleaser/goreleaser"
	} else {
		repo = os.Args[1]
	}
	if err := build(context.Background(), repo); err != nil {
		fmt.Println(err)
	}
}

func build(ctx context.Context, repoURL string) error {
	fmt.Printf("Building %s\n", repoURL)

	// define build matrix
	oses := []string{"linux", "darwin"}
	arches := []string{"amd64", "arm64"}
	goVersions := []string{"1.18", "1.19"}

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		return err
	}
	defer client.Close()

	// clone repository with Dagger
	repo := client.Git(repoURL)
	src := repo.Branch("main").Tree()

	outputDirectory := client.Directory()

	for _, version := range goVersions {
		// get `golang` image for specified Go version
		imageTag := fmt.Sprintf("golang:%s", version)
		golang := client.Container().
			From(imageTag).
			WithDirectory("/src", src).
			WithWorkdir("/src")

		for _, goos := range oses {
			for _, goarch := range arches {
				// create a directory for each os, arch and version
				path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch)

				// set GOARCH and GOOS in the build environment
				build := golang.WithEnvVariable("GOOS", goos).
					WithEnvVariable("GOARCH", goarch).
					WithExec([]string{"go", "build", "-o", path})

				// build application
				outputDirectory = outputDirectory.WithDirectory(path, build.Directory(path))
			}
		}
	}
	_, err = outputDirectory.Export(ctx, ".")
	return err
}
