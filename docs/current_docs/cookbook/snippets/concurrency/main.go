package main

import (
	"context"
	"crypto/rand"
	"log"
	"math/big"
	"os"

	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
)

func longTimeTask(ctx context.Context, c *dagger.Client) error {
	sleepTime, err := rand.Int(rand.Reader, big.NewInt(10))
	if err != nil {
		return err
	}

	_, err = c.Container().From("alpine").
		WithExec([]string{"sleep", sleepTime.String()}).
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
