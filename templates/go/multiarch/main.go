package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.dagger.io/dagger/sdk/go/dagger"
	"go.dagger.io/dagger/sdk/go/dagger/api"
	"golang.org/x/sync/errgroup"
)

func main() {
	repo := "https://github.com/kpenfound/greetings-api.git" // Default repo to build
	if len(os.Args) > 1 {                                    // Optionally pass in a git repo as a command line argument
		repo = os.Args[1]
	}
	if err := build(repo); err != nil {
		fmt.Println(err)
	}
	filepath.Walk("build", func(name string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			fmt.Println(name)
		}
		return nil
	})
}

func build(repoUrl string) error {
	ctx := context.Background()
	g, ctx := errgroup.WithContext(ctx)

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

	// clone the specified git repo
	repo := client.Core().Git(repoUrl)
	src, err := repo.Branch("main").Tree().ID(ctx)
	if err != nil {
		return err
	}

	// reference to the current working directory
	workdir := client.Core().Host().Workdir()

	for _, version := range goVersions {
		// Get golang image and mount go source
		imageTag := fmt.Sprintf("golang:%s", version)
		golang := client.Core().Container().From(api.ContainerAddress(imageTag))
		golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

		// Run matrix builds in parallel
		for _, goos := range oses {
			for _, goarch := range arches {
				goos, goarch, version := goos, goarch, version // closures
				g.Go(func() error {
					return buildOsArch(ctx, golang, workdir, goos, goarch, version)
				})
			}
		}
	}
	if err := g.Wait(); err != nil {
		return err
	}
	return nil
}

func buildOsArch(ctx context.Context, builder *api.Container, workdir *api.HostDirectory, goos string, goarch string, version string) error {
	fmt.Printf("Building %s %s with go %s\n", goos, goarch, version)

	// Create the output path for the build
	path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch)
	outpath := filepath.Join(".", path)
	err := os.MkdirAll(outpath, os.ModePerm)
	if err != nil {
		return err
	}

	// Set GOARCH and GOOS and build
	build := builder.WithEnvVariable("GOOS", goos)
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
	return err
}
