package main

import (
	"context"
	"fmt"
	"os"
	"log"

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
	contents, err := client.Container().
			From("alpine:latest").
			WithDirectory("/src", project).
			WithWorkdir("/src").
			WithExec([]string{"ls", "/src"}).
			Stdout(ctx)
	if err != nil {
        log.Println(err)
        return
  }

	fmt.Println(contents)
}
