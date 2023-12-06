package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	// initialize Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// set secret as string value
	secret := client.SetSecret("password", "DOCKER-HUB-PASSWORD")

	// create container
	c := client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
		From("nginx:1.23-alpine").
		WithNewFile("/usr/share/nginx/html/index.html", dagger.ContainerWithNewFileOpts{
			Contents:    "Hello from Dagger!",
			Permissions: 0o400,
		})

	// use secret for registry authentication
	addr, err := c.
		WithRegistryAuth("docker.io", "DOCKER-HUB-USERNAME", secret).
		Publish(ctx, "DOCKER-HUB-USERNAME/my-nginx")
	if err != nil {
		panic(err)
	}

	// print result
	fmt.Println("Published at:", addr)
}
