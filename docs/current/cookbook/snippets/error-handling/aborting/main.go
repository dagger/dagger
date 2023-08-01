package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return fmt.Errorf("dagger connect: %w", err)
	}
	defer client.Close()

	err = Test(ctx, client)
	if err != nil {
		return fmt.Errorf("test pipeline: %w", err)
	}

	fmt.Println("Test passed!")
	return nil
}

func Test(ctx context.Context, client *dagger.Client) error {
	_, err := client.
		Container().
		From("alpine").
		WithExec([]string{"false"}).
		Sync(ctx)
	return err
}
