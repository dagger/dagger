package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// use a node:16-slim container
	// get version
	// execute
	golang := client.Container().From("golang:1.19").WithExec([]string{"go", "version"})

	version, err := golang.Stdout(ctx)
	if err != nil {
		panic(err)
	}

	// print output
	fmt.Println("Hello from Dagger and " + version)
}
