package main

import (
	"context"

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

	// setup container and
	// define environment variables
	_, err = client.
		Container().
		From("docker").
		WithUnixSocket("/var/run/docker.sock", client.Host().UnixSocket("/var/run/docker.sock")).
		WithExec([]string{"docker", "run", "--rm", "alpine", "uname", "-a"}).
		Sync(ctx)

	if err != nil {
		panic(err)
	}
}
