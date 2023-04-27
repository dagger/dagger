package main

import (
	"context"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr), dagger.WithWorkdir("."))
	if err != nil {
		log.Println(err)
		return
	}

	defer client.Close()
}
