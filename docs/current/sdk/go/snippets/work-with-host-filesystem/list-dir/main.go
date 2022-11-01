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
	entries, err := client.Host().Workdir().Entries(ctx)
	// highlight-end
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(entries)
}
