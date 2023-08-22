package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	if os.Getenv("GH_SECRET") == "" {
		panic("Environment variable GH_SECRET is not set")
	}

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// read secret from host variable
	secret := client.SetSecret("gh-secret", os.Getenv("GH_SECRET"))

	// set context directory for Dockerfile build
	contextDir := client.Host().Directory(".")

	// build using Dockerfile
	// specify secrets for Dockerfile build
	// secrets will be mounted at /run/secrets/[secret-name]
	out, err := contextDir.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "Dockerfile",
			Secrets:    []*dagger.Secret{secret},
		}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}
