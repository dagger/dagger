package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
	"github.com/mitchellh/go-homedir"
)

func main() {
	os.Setenv("DAGGER_HOST", "tcp://localhost:8080")

	ctx := context.Background()

	opts := []dagger.ClientOpt{
		dagger.WithWorkdir("."),
	}

	daggerSrc, err := homedir.Expand("~/src/dagger")
	if err != nil {
		panic(err)
	}

	log.Println("source:", daggerSrc)

	c, err := dagger.Connect(ctx, opts...)
	if err != nil {
		panic(err)
	}
	defer c.Close()

	contents, err := c.Container().
		From("alpine:edge").
		WithMountedDirectory("/src/dagger", c.Host().Directory(daggerSrc)).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"ls", "-al", "/src/dagger"},
		}).Stdout().Contents(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Print(contents)

	bassSrc, err := homedir.Expand("~/src/bass")
	if err != nil {
		panic(err)
	}

	contents, err = c.Container().
		From("alpine:edge").
		WithMountedDirectory("/src/bass", c.Host().Directory(bassSrc)).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"ls", "-al", "/src/bass"},
		}).Stdout().Contents(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Print(contents)

	exitCode, err := c.Container().
		From("alpine:edge").
		WithMountedDirectory("/src/bass", c.Host().Directory(bassSrc)).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"sleep", "30"},
		}).ExitCode(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(exitCode)
}
