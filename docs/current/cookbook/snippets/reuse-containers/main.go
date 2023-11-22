package main

import (
	"context"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// build container in one pipeline
	c, err := client.Pipeline("Test").
		Container().
		From("alpine").
		WithExec([]string{"apk", "add", "curl"}).
		Sync(ctx)
	if err != nil {
		panic(err)
	}

	// get container ID
	cid, err := c.ID(ctx)
	if err != nil {
		panic(err)
	}

	// use container in another pipeline via its ID
	client.Container(dagger.ContainerOpts{ID: cid}).
		Pipeline("Build").
		WithExec([]string{"curl", "https://dagger.io"}).Sync(ctx)
}
