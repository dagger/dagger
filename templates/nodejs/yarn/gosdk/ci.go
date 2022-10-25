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
	src, err := client.Host().Workdir().Read().ID(ctx)
	if err != nil {
		return err
	}

	// initialize new container from yarn image
	yarn := client.Container().From("yarnpkg/node-yarn")

	// mount source directory to /src
	yarn = yarn.WithMountedDirectory("/src", src).WithWorkdir("/src")

	// execute yarn test command
	yarn = yarn.Exec(dagger.ContainerExecOpts{
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
	yarn = yarn.Exec(dagger.ContainerExecOpts{
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
