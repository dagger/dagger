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

	src := client.Host().Directory(".") // get the projects source directory

	yarn := client.Container().From("yarnpkg/node-yarn"). // initialize new container from yarn image
								WithMountedDirectory("/src", src).WithWorkdir("/src"). // mount source directory to /src
								WithExec([]string{"yarn", "test"})                     // execute yarn test command

	// get test output
	test, err := yarn.Stdout(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(test)

	// execute build command and get build output
	build, err := yarn.WithExec([]string{"yarn", "build"}).Stdout(ctx)
	if err != nil {
		return err
	}
	// print output to console
	fmt.Println(build)

	return nil
}
