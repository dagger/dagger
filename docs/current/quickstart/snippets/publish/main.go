package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
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

	test := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"})

	_, err = test.WithExec([]string{"npm", "run", "build"}).
		Directory("./build").
		Export(ctx, "./build")

	if err != nil {
		panic(err)
	}

	ref, err := client.Container().
		From("nginx").
		WithDirectory("/usr/src/nginx", client.Host().Directory("./build")).
		Publish(ctx, fmt.Sprintf("ttl.sh/hello-dagger-%.0f", math.Floor(rand.Float64()*10000000))) //#nosec
	if err != nil {
		panic(err)
	}

	fmt.Printf("Published image to :%s\n", ref)
}
