package dagger_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	out, err := alpine.Exec(dagger.ContainerExecOpts{
		Args: []string{"cat", "/etc/alpine-release"},
	}).Stdout().Contents(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(out)

	// Output: 3.16.2
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

func ExampleContainer_Build() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	repo := client.Git("https://github.com/dagger/dagger").
		Tag("v0.3.0").
		Tree()

	daggerImg := client.Container().Build(repo)

	out, err := daggerImg.Exec(dagger.ContainerExecOpts{
		Args: []string{"version"},
	}).Stdout().Contents(ctx)
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

	container := client.Container().From("alpine:3.16.2")

	container = container.WithEnvVariable("FOO", "bar")

	out, err := container.Exec(dagger.ContainerExecOpts{
		Args: []string{"sh", "-c", "echo $FOO"},
	}).Stdout().Contents(ctx)
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
		WithNewFile("hello.txt", dagger.DirectoryWithNewFileOpts{
			Contents: "Hello, world!",
		}).
		WithNewFile("goodbye.txt", dagger.DirectoryWithNewFileOpts{
			Contents: "Goodbye, world!",
		})

	container := client.Container().From("alpine:3.16.2")

	container = container.WithMountedDirectory("/mnt", dir)

	out, err := container.Exec(dagger.ContainerExecOpts{
		Args: []string{"ls", "/mnt"},
	}).Stdout().Contents(ctx)
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

	cacheKey := "example-" + time.Now().Format(time.RFC3339)

	cache := client.CacheVolume(cacheKey)

	container := client.Container().From("alpine:3.16.2")

	container = container.WithMountedCache("/cache", cache)

	var out string
	for i := 0; i < 5; i++ {
		out, err = container.Exec(dagger.ContainerExecOpts{
			Args: []string{
				"sh", "-c",
				"echo $0 >> /cache/x.txt; cat /cache/x.txt",
				strconv.Itoa(i),
			},
		}).Stdout().Contents(ctx)
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
		WithNewFile("hello.txt", dagger.DirectoryWithNewFileOpts{
			Contents: "Hello, world!",
		}).
		WithNewFile("goodbye.txt", dagger.DirectoryWithNewFileOpts{
			Contents: "Goodbye, world!",
		})

	entries, err := dir.Entries(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(entries)

	// Output: [goodbye.txt hello.txt]
}

func ExampleHost_Workdir() {
	wd, err := os.MkdirTemp("", "dagger-example-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(wd)

	err = os.WriteFile(filepath.Join(wd, "hello.txt"), []byte("Hello, world!"), 0600)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithWorkdir(wd), dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	contents, err := client.Host().Workdir().File("hello.txt").Contents(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(contents)

	// Output: Hello, world!
}

// func ExampleHost_EnvVariable() {
// 	ctx := context.Background()
// 	client, err := dagger.Connect(ctx)
// 	if err != nil {
// 		panic(err)
// 	}
// 	defer client.Close()

// 	os.Setenv("SEKRIT", "hunter2")

// 	secretID, err := client.Host().EnvVariable("SEKRIT").Secret().ID(ctx)
// 	if err != nil {
// 		panic(err)
// 	}

// 	alpine := client.Container().From("alpine:3.16.2")
// 	leaked, err := alpine.
// 		WithSecretVariable("PASSWORD", secretID).
// 		Exec(dagger.ContainerExecOpts{
// 			Args: []string{"sh", "-c", "echo $PASSWORD"},
// 		}).
// 		Stdout().Contents(ctx)
// 	if err != nil {
// 		panic(err)
// 	}

// 	fmt.Println(leaked)

// 	// Output: hunter2
// }
