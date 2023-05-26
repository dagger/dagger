package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"dagger.io/dagger"
	"github.com/google/uuid"
)

func main() {
	// create Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// invalidate cache to force execution
	// of second WithExec() operation
	output, err := client.Pipeline("test").
		Container().
		From("alpine").
		WithExec([]string{"apk", "add", "curl"}).
		WithEnvVariable("CACHEBUSTER", uuid.New().String()).
		WithExec([]string{"apk", "add", "zip"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(output)
}
