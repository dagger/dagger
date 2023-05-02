package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	entries, err := client.Host().Directory(".").Entries(ctx)
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(entries)
}
