package main

import (
	"context"
	"fmt"

	"os"

	"dagger.io/dagger"
)

func main() {
	// create Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// setup container with docker socket
	ctr := client.
		Container().
		From("docker").
		WithUnixSocket("/var/run/docker.sock", client.Host().UnixSocket("/var/run/docker.sock")).
		WithExec([]string{"docker", "run", "--rm", "alpine", "uname", "-a"})

	// print docker run
	out, err := ctr.Stdout(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
}
