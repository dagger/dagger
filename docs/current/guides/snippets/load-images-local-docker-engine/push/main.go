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

	// use NGINX container
	// add new webserver index page
	c := client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
		From("nginx:1.23-alpine").
		WithNewFile("/usr/share/nginx/html/index.html", dagger.ContainerWithNewFileOpts{
			Contents:    "Hello from Dagger!",
			Permissions: 0o400,
		})

	// publish to local registry
	addr, err := c.Publish(ctx, "127.0.0.1:5000/my-nginx:1.0")

	if err != nil {
		panic(err)
	}

	// print result
	fmt.Println("Published at:", addr)
}
