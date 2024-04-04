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

	out, err := client.Container().
		From("nvidia/cuda:11.7.1-base-ubuntu20.04").
		WithExec([]string{"nvidia-smi", "-L"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println("available GPUs", out)
}
