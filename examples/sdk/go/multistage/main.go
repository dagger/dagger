package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"

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

	project := client.Git("https://github.com/dagger/dagger").Branch("main").Tree()

	build := client.Container().
		From("golang:1.20").
		WithDirectory("/src", project).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "./cmd/dagger"})

	prodImage := client.Container().
		From("cgr.dev/chainguard/wolfi-base:latest").
		WithFile("/bin/dagger", build.File("/src/dagger")).
		WithEntrypoint([]string{"/bin/dagger"})

	// generate uuid for ttl.sh publish
	id := uuid.New()
	tag := fmt.Sprintf("ttl.sh/dagger-%s:1h", id.String())

	_, err = prodImage.Publish(ctx, tag)
}
