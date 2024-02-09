package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	// create a Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	platforms := []dagger.Platform{"linux/amd64", "linux/arm64"}

	project := client.Git("https://github.com/dagger/dagger").Branch("main").Tree()

	cache := client.CacheVolume("gomodcache")

	buildArtifacts := client.Directory()

	for _, platform := range platforms {
		build := client.Container(dagger.ContainerOpts{Platform: platform}).
			From("golang:1.22.0-bullseye").
			WithDirectory("/src", project).
			WithWorkdir("/src").
			WithMountedCache("/cache", cache).
			WithEnvVariable("GOMODCACHE", "/cache").
			WithExec([]string{"go", "build", "./cmd/dagger"})

		buildArtifacts = buildArtifacts.WithFile(fmt.Sprintf("%s/dagger", platform), build.File("/src/dagger"))
	}

	_, err = buildArtifacts.Export(ctx, ".")
	if err != nil {
		panic(err)
	}
}
