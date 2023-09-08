package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	ctr := client.
		Container().
		From("alpine")

	// breaks the chain!
	ctr = AddMounts(ctr, client)

	out, err := ctr.
		WithExec([]string{"ls"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}

func AddMounts(ctr *dagger.Container, client *dagger.Client) *dagger.Container {
	return ctr.
		WithMountedDirectory("/foo", client.Host().Directory("/tmp/foo")).
		WithMountedDirectory("/bar", client.Host().Directory("/tmp/bar"))
}
