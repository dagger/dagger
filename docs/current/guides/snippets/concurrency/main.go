package main

import (
	"context"
	"golang.org/x/sync/errgroup"
	"log"
	"math/rand"
	"os"
	"strconv"

	"dagger.io/dagger"
)

func longTimeTask(ctx context.Context, c *dagger.Client) error {
	_, err := c.Container().From("alpine").
		WithExec([]string{"sleep", strconv.Itoa(rand.Intn(10))}).
		WithExec([]string{"echo", "task done"}).
		Sync(ctx)

	return err
}

func main() {
	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	// Create err-group to handle error
	eg, gctx := errgroup.WithContext(ctx)

	// Launch task 1
	eg.Go(func() error {
		return longTimeTask(gctx, client)
	})

	// Launch task 2
	eg.Go(func() error {
		return longTimeTask(gctx, client)
	})

	// Launch task 3
	eg.Go(func() error {
		return longTimeTask(gctx, client)
	})

	// Wait for each task to be completed
	err = eg.Wait()
	if err != nil {
		panic(err)
	}
}
