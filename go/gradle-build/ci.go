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

	src := client.Host().Directory(".")	// get the projects source directory

	gradle := client.Container().From("gradle:latest"). // Build an gradle image with gradle and bash installed
		WithMountedDirectory("/src", src).WithWorkdir("/src").	// mount source directory to /src
		WithExec([]string{"gradle", "build"})	// execute gradle build command

	// get gradle output
	out, err := gradle.Stdout(ctx)
	if err != nil {
		return err
	}

	// print output to console
	fmt.Println(out)

	return nil
}
