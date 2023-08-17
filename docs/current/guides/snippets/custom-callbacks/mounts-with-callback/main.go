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

	out, err := client.
		Container().
		From("alpine").
		With(Mounts(client)).
		WithExec([]string{"ls"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}

func Mounts(client *dagger.Client) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedDirectory("/foo", client.Host().Directory("/tmp/foo")).
			WithMountedDirectory("/bar", client.Host().Directory("/tmp/bar"))
	}
}
