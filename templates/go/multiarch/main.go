package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.dagger.io/dagger/sdk/go/dagger"
	"go.dagger.io/dagger/sdk/go/dagger/api"
)

type platform struct {
	os   string
	arch string
}

func main() {
	err := build()
	if err != nil {
		fmt.Println(err)
	}
}

func build() error {
	ctx := context.Background()

	// Our build matrix
	oses := []string{"linux", "darwin"}
	arches := []string{"amd64", "arm64"}
	goVersions := []string{"1.18", "1.19"}

	// create a Dagger client
	client, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	// get the projects source directory
	repo := client.Core().Git("https://github.com/kpenfound/greetings-api.git")
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

		// TODO: parallel
		for _, goos := range oses {
			for _, goarch := range arches {
				fmt.Printf("Building %s %s with go %s\n", goos, goarch, version)

				// Write the build output to the host
				path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch)
				outpath := filepath.Join(".", path)
				err = os.MkdirAll(outpath, os.ModePerm)
				if err != nil {
					return err
				}

				// Set GOARCH and GOOS
				build := golang.WithEnvVariable("GOOS", goos)
				build = build.WithEnvVariable("GOARCH", goarch)

				build = build.Exec(api.ContainerExecOpts{
					Args: []string{"go", "build", "-o", path},
				})

				// Get build output from builder
				output, err := build.Directory(path).ID(ctx)
				if err != nil {
					return err
				}

				// Write the build output to the host
				_, err = workdir.Write(ctx, output, api.HostDirectoryWriteOpts{Path: path})
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}
