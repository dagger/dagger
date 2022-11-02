package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Must pass in a Git repository to build")
		os.Exit(1)
	}
	repo := os.Args[1]
	if err := build(context.Background(), repo); err != nil {
		fmt.Println(err)
	}
}

func build(ctx context.Context, repoURL string) error {
	fmt.Printf("Building %s\n", repoURL)

	// highlight-start
	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		return err
	}
	defer client.Close()

	// clone repository with Dagger
	repo := client.Git(repoURL)
	src := repo.Branch("main").Tree()

	// get `golang` image
	golang := client.Container().From("golang:latest")

	// mount cloned repository into `golang` image
	golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

	// define the application build command
	path := "build/"
	golang = golang.Exec(dagger.ContainerExecOpts{
		Args: []string{"go", "build", "-o", path},
	})

	// get reference to build output directory in container
	output := golang.Directory(path)

	// write contents of container build/ directory to the host
	_, err = output.Export(ctx, path)
	// highlight-end
	if err != nil {
		return err
	}

	return nil
}
