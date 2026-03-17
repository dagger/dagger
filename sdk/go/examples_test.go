package dagger_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger"
)

func ExampleContainer() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	alpine := client.Container().From("alpine:3.16.2")

	out, err := alpine.WithExec([]string{"cat", "/etc/alpine-release"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)

	// Output: 3.16.2
}

func ExampleContainer_With() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	alpine := client.Container().From("alpine:3.16.2").
		With(func(c *dagger.Container) *dagger.Container {
			return c.WithEnvVariable("FOO", "bar")
		})

	out, err := alpine.WithExec([]string{"printenv", "FOO"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
	// Output: bar
}

func ExampleGitRepository() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	readme, err := client.Git("https://github.com/dagger/dagger").
		Tag("v0.3.0").
		Tree().File("README.md").Contents(ctx)
	if err != nil {
		panic(err)
	}

	lines := strings.Split(strings.TrimSpace(readme), "\n")
	fmt.Println(lines[0])

	// Output: ## What is Dagger?
}

func ExampleDirectory_DockerBuild() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	daggerImg := client.Git("https://github.com/dagger/dagger").
		Tag("v0.3.0").
		Tree().
		DockerBuild()

	out, err := daggerImg.WithExec([]string{"dagger", "version"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}

	words := strings.Split(strings.TrimSpace(out), " ")
	fmt.Println(words[0])

	// Output: dagger
}

func ExampleContainer_WithEnvVariable() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	out, err := client.
		Container().
		From("alpine:3.16.2").
		WithEnvVariable("FOO", "bar").
		WithExec([]string{"sh", "-c", "echo $FOO"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)

	// Output: bar
}

func ExampleContainer_WithMountedDirectory() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	dir := client.Directory().
		WithNewFile("hello.txt", "Hello, world!").
		WithNewFile("goodbye.txt", "Goodbye, world!")

	out, err := client.
		Container().
		From("alpine:3.16.2").
		WithMountedDirectory("/mnt", dir).
		WithExec([]string{"ls", "/mnt"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%q", out)

	// Output: "goodbye.txt\nhello.txt\n"
}

func ExampleContainer_WithMountedCache() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	cacheKey := "example-cache-" + rand.Text()

	cache := client.CacheVolume(cacheKey)

	container := client.Container().From("alpine:3.16.2")

	container = container.WithMountedCache("/cache", cache)

	filename := time.Now().Format("2006-01-02-15-04-05")
	echoCmd := fmt.Sprintf("echo $0 >> /cache/%[1]v.txt; cat /cache/%[1]v.txt", filename)

	var out string
	for i := 0; i < 5; i++ {
		out, err = container.
			WithExec([]string{"sh", "-c", echoCmd, strconv.Itoa(i)}).
			Stdout(ctx)
		if err != nil {
			panic(err)
		}
	}

	fmt.Printf("%q", out)

	// Output: "0\n1\n2\n3\n4\n"
}

func ExampleDirectory() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	dir := client.Directory().
		WithNewFile("hello.txt", "Hello, world!").
		WithNewFile("goodbye.txt", "Goodbye, world!")

	entries, err := dir.Entries(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(entries)

	// Output: [goodbye.txt hello.txt]
}

func ExampleHost_Directory() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithWorkdir("."))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	readme, err := client.Host().Directory(".").File("README.md").Contents(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%v\n", strings.Contains(readme, "Dagger"))

	// Output: true
}
