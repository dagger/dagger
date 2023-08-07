package main

import (
	"context"
	"fmt"
	"log"
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

	// get repository at specified branch
	project := client.
		Git("https://github.com/dagger/dagger").
		Branch("main").
		Tree()

	// return container with repository
	// at /src path
	// including all files except files beginning with .git
	contents, err := client.Container().
		From("alpine:latest").
		WithDirectory("/src", project, dagger.ContainerWithDirectoryOpts{
			Include: []string{"*"},
			Exclude: []string{".git*"},
		}).
		WithWorkdir("/src").
		WithExec([]string{"ls", "/src"}).
		Stdout(ctx)
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(contents)
}
