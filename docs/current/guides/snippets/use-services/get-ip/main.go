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

	// get IP address of service container
	val, err := client.Container().
		From("alpine").
		WithExec([]string{"sh", "-c", "ip route | grep src"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(val)
}
