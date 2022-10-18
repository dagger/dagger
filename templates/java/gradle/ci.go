package main

import (
	"context"
	"fmt"

	"go.dagger.io/dagger/sdk/go/dagger"
	"go.dagger.io/dagger/sdk/go/dagger/api"
)

func main() {
	err := doCi()
	if err != nil {
		fmt.Println(err)
	}
}

func doCi() error {
	ctx := context.Background()

	// create a Dagger client
	client, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	// get the projects source directory
	src, err := client.Core().Host().Workdir().Read().ID(ctx)
	if err != nil {
		return err
	}

	// Build an gradle image with gradle and bash installed
	gradle := client.Core().Container().From("gradle:latest")

	// mount source directory to /src
	gradle = gradle.WithMountedDirectory("/src", src).WithWorkdir("/src")

	// execute gradle build command
	gradle = gradle.Exec(api.ContainerExecOpts{
		Args: []string{"gradle", "build"},
	})

	// get gradle output
	out, err := gradle.Stdout().Contents(ctx)
	if err != nil {
		return err
	}

	// print output to console
	fmt.Println(out)

	return nil
}
