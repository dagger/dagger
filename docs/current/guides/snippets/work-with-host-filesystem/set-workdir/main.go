package main

import (
	"context"
	"log"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// highlight-start
	client, err := dagger.Connect(ctx, dagger.WithWorkdir("."))
	// highlight-end
	if err != nil {
		log.Println(err)
		return
	}

	defer client.Close()
}
