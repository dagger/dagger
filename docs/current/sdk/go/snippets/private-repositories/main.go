package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Private repository with a README.md file at the root.
	readme, err := client.
		Git("git@private-repository.git").
		Branch("main").
		Tree().
		File("README.md").
		Contents(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println("readme", readme)
}
