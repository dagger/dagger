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

	// initialize new container from yarn image
	yarn := client.Core().Container().From("yarnpkg/node-yarn")

	// mount source directory to /src
	yarn = yarn.WithMountedDirectory("/src", src).WithWorkdir("/src")

	// execute yarn test command
	yarn = yarn.Exec(api.ContainerExecOpts{
		Args: []string{"yarn", "test"},
	})

	// get test output
	test, err := yarn.Stdout().Contents(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(test)

	// execute build command
	yarn = yarn.Exec(api.ContainerExecOpts{
		Args: []string{"yarn", "build"},
	})

	// get build output
	build, err := yarn.Stdout().Contents(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(build)

	return nil
}
