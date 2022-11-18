package main

import (
	"context"
	"fmt"
	"log"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithWorkdir("."))
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	// highlight-start
	contents, err := client.Container().
		From("alpine:latest").
		WithMountedDirectory("/host", client.Host().Directory(".")).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"ls", "/host"},
		}).Stdout().Contents(ctx)
	// highlight-end
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(contents)
}
