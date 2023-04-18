package main

import (
	"context"
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
}
