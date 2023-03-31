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

	// export to host filesystem
	val, err := c.Export(ctx, "/tmp/my-nginx.tar")
	if err != nil {
		panic(err)
	}

	// print result
	fmt.Println("Exported image: ", val)
}
