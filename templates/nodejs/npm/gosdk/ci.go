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

	// initialize new container from npm image
	npm := client.Core().Container().From("node")

	// mount source directory to /src
	npm = npm.WithMountedDirectory("/src", src).WithWorkdir("/src")

	// execute npm install
	npm = npm.Exec(api.ContainerExecOpts{
		Args: []string{"npm", "install"},
	})

	// execute npm test command
	npm = npm.Exec(api.ContainerExecOpts{
		Args: []string{"npm", "run", "test"},
	})

	// get test output
	test, err := npm.Stdout().Contents(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(test)

	// execute build command
	npm = npm.Exec(api.ContainerExecOpts{
		Args: []string{"npm", "run", "build"},
	})

	// get build output
	build, err := npm.Stdout().Contents(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(build)

	return nil
}
