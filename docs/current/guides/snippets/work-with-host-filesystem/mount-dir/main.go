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
		WithDirectory("/host", client.Host().Directory(".")).
		WithExec([]string{"ls", "/host"}).
		Stdout(ctx)
	// highlight-end
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(contents)
}
