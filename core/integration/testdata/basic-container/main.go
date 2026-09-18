package main

import (
	"context"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

func main() {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer c.Close()

	_, err = core.NewQuery(c).Container().
		From("alpine:3.16.2").
		WithExec([]string{"echo", "Hello, world!"}).
		Sync(ctx)
	if err != nil {
		panic(err)
	}
}
