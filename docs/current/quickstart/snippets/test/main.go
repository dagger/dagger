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
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	hostSourceDir := client.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"node_modules/", "ci/"},
	})

	source := client.Container().
		From("node:16").
		WithMountedDirectory("/src", hostSourceDir)

	runner := source.WithWorkdir("/src").
		WithExec([]string{"npm", "install"})

	out, err := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"}).
		Stderr(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
}
