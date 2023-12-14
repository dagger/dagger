package main

import (
	"context"
	"fmt"
	"os"
	"time"

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

	// create and publish image with annotations
	ctr := client.Container().
		From("alpine").
		WithLabel("org.opencontainers.image.title", "my-alpine").
		WithLabel("org.opencontainers.image.version", "1.0").
		WithLabel("org.opencontainers.image.created", time.Now().String()).
		WithLabel("org.opencontainers.image.source", "https://github.com/alpinelinux/docker-alpine").
		WithLabel("org.opencontainers.image.licenses", "MIT")

	addr, err := ctr.Publish(ctx, "ttl.sh/my-alpine")

	// note: some registries (e.g. ghcr.io) may require explicit use
	// of Docker mediatypes rather than the default OCI mediatypes
	// addr, err := ctr.Publish(ctx, "ttl.sh/my-alpine", dagger.ContainerPublishOpts{
	//   MediaTypes: dagger.Dockermediatypes,
	// })

	if err != nil {
		panic(err)
	}

	fmt.Println(addr)
}
