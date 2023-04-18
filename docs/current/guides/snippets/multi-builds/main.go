// Create a multi-build pipeline for a Go application.
package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	println("Building with Dagger")

	// define build matrix
	geese := []string{"linux", "darwin"}
	goarches := []string{"amd64", "arm64"}

	ctx := context.Background()
	// initialize dagger client
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}

	// get reference to the local project
	src := c.Host().Directory(".")

	// create empty directory to put build outputs
	outputs := c.Directory()

	golang := c.Container().
		// get golang image
		From("golang:latest").
		// mount source code into golang image
		WithDirectory("/src", src).
		WithWorkdir("/src")

	for _, goos := range geese {
		for _, goarch := range goarches {
			// create a directory for each OS and architecture
			path := fmt.Sprintf("build/%s/%s/", goos, goarch)

			build := golang.
				// set GOARCH and GOOS in the build environment
				WithEnvVariable("GOOS", goos).
				WithEnvVariable("GOARCH", goarch).
				WithExec([]string{"go", "build", "-o", path})

			// add build to outputs
			outputs = outputs.WithDirectory(path, build.Directory(path))
		}
	}

	// write build artifacts to host
	ok, err := outputs.Export(ctx, ".")
	if err != nil {
		panic(err)
	}

	if !ok {
		panic("did not export files")
	}
}
