package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// check for required variables in host environment
	vars := []string{"REGISTRY_ADDRESS", "REGISTRY_USERNAME", "REGISTRY_PASSWORD"}
	for _, v := range vars {
		if os.Getenv(v) == "" {
			log.Fatalf("Environment variable %s is not set", v)
		}
	}

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// set registry password as Dagger secret
	secret := client.SetSecret("password", os.Getenv("REGISTRY_PASSWORD"))

	// get reference to the project directory
	source := client.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci", "node_modules"},
	})

	// use a node:18-slim container
	node := client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
		From("node:18-slim")

	// mount the project directory
	// at /src in the container
	// set the working directory in the container
	// install application dependencies
	// build application
	// set default arguments
	app := node.WithDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"npm", "install"}).
		WithExec([]string{"npm", "run", "build"}).
		WithDefaultArgs([]string{"npm", "start"})

	// publish image to registry
	// at registry path [registry-username]/myapp
	// print image address
	address, err := app.WithRegistryAuth(os.Getenv("REGISTRY_ADDRESS"), os.Getenv("REGISTRY_USERNAME"), secret).
		Publish(ctx, fmt.Sprintf("%s/myapp", os.Getenv("REGISTRY_USERNAME")))
	if err != nil {
		panic(err)
	}
	fmt.Println("Published image to:", address)
}
