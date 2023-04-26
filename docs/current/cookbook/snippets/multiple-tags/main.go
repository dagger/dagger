package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	// define tags
	tags := [4]string{"latest", "1.0-alpine", "1.0", "1.0.0"}

	// create Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// set secret as string value
	secret := client.SetSecret("password", "DOCKER-HUB-PASSWORD")

	// create and publish image with annotations
	ctr := client.Container().
		From("alpine")

	for i := 0; i < len(tags); i++ {
		addr, err := ctr.
			WithRegistryAuth("docker.io", "DOCKER-HUB-USERNAME", secret).
			Publish(ctx, "DOCKER-HUB-USERNAME/my-alpine:"+tags[i])
		if err != nil {
			panic(err)
		}
		fmt.Println("Published at:", addr)
	}
}
