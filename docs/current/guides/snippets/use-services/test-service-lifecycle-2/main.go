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

	// set value
	setter, err1 := redisCLI.
		WithExec([]string{"set", "foo", "abc"}).
		Stdout(ctx)

	if err1 != nil {
		panic(err1)
	}

	fmt.Println(setter)

	// get value
	getter, err2 := redisCLI.
		WithExec([]string{"get", "foo"}).
		Stdout(ctx)

	if err2 != nil {
		panic(err2)
	}

	fmt.Println(getter)
}
