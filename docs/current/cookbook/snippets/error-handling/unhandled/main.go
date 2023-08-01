package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	fmt.Println("Testing... ")

	err = Test(ctx, client)
	if err != nil {
		panic(err)
	}

	fmt.Println("PASS")
}

func Test(ctx context.Context, client *dagger.Client) error {
	_, err := client.
		Container().
		From("alpine").
		WithExec([]string{"true"}).
		Sync(ctx)
	return err
}
