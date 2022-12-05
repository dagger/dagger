package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
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
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		return err
	}
	defer client.Close()

	// get the projects source directory
	src := client.Host().Directory(".")

	// Build an gradle image with gradle and bash installed
	gradle := client.Container().From("gradle:latest")

	// mount source directory to /src
	gradle = gradle.WithMountedDirectory("/src", src).WithWorkdir("/src")

	// execute gradle build command
	gradle = gradle.WithExec([]string{"gradle", "build"})

	// get gradle output
	out, err := gradle.Stdout(ctx)
	if err != nil {
		return err
	}

	// print output to console
	fmt.Println(out)

	return nil
}
