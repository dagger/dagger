package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"

	"main/alpine"
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
	pipeline := alpine.New(client)

	// Call version function
	fmt.Println(pipeline.Version(ctx))
}
