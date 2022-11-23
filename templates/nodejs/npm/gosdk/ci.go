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

	// initialize new container from npm image
	npm := client.Container().From("node")

	// mount source directory to /src
	npm = npm.WithMountedDirectory("/src", src).WithWorkdir("/src")

	// execute npm install
	npm = npm.WithExec([]string{"npm", "install"})

	// execute npm test command
	npm = npm.WithExec([]string{"npm", "run", "test"})

	// get test output
	test, err := npm.Stdout(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(test)

	// execute build command
	npm = npm.WithExec([]string{"npm", "run", "build"})

	// get build output
	build, err := npm.Stdout(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(build)

	return nil
}
