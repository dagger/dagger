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

	// pass client to method imported from another module
	fmt.Println(alpine.Version(client))
}
