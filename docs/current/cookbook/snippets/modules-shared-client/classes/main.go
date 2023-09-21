package main

import (
	"context"
	"dagger.io/dagger"
	"fmt"
	"os"

	"main/pipelines"
)

func main() {
	ctx := context.Background()

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Create pipeline structure imported from another module passing the client
	pipeline := pipelines.New(client)

	// Call version function
	fmt.Println(pipeline.Version(ctx))
}
