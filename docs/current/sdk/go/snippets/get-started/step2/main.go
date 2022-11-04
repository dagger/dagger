package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
)

func main() {
	if err := build(context.Background()); err != nil {
		fmt.Println(err)
	}
}

func build(ctx context.Context) error {
	fmt.Println("Building with Dagger")

	// initialize Dagger client
	client, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	return nil
}
