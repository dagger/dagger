package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))

	if err != nil {
		panic(err)
	}
	defer client.Close()

	// create Redis service container
	redisSrv := client.Container().
		From("redis").
		WithExposedPort(6379).
		WithExec(nil)

	// create Redis client container
	redisCLI := client.Container().
		From("redis").
		WithServiceBinding("redis-srv", redisSrv).
		WithEntrypoint([]string{"redis-cli", "-h", "redis-srv"})

	// set and get value
	val, err := redisCLI.
		WithExec([]string{"set", "foo", "abc"}).
		WithExec([]string{"get", "foo"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(val)
}
