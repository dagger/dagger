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

	// get hostname of service container via API
	val, err := client.Container().
		From("python").
		WithExec([]string{"python", "-m", "http.server"}).
		Hostname(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(val)
}
