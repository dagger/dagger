package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))

	if err != nil {
		panic(err)
	}
	defer client.Close()

	// get hostname of service container
	val, err := client.Container().
		From("alpine").
		WithExec([]string{"hostname"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(val)
}
