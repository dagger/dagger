package main

import (
	"context"
	"fmt"
	"path/filepath"

	"dagger.io/dagger"
)

func main() {
	dagger.Serve(
		Build,
		Test,
		Ci,
	)
}

func Ci(ctx context.Context, inputPayload string) (string, error) {
	fmt.Println(inputPayload)
	return inputPayload, nil
}

// Build the go binary from the given go repo, branch, and subpath. Silly right now due to lack of host export, but proves concept
func Build(ctx context.Context, repo, branch, subpath string) (string, error) {
	c, err := dagger.Connect(ctx)
	if err != nil {
		return "", err
	}
	defer c.Close()

	binpath := filepath.Join("./bin", filepath.Base(subpath))
	return baseContainer(c, repo, branch).
		WithExec([]string{"go", "build", "-v", "-x", "-o", binpath, "./" + subpath}).
		Stderr(ctx)
}

// Test the go binary from given go repo, branch, and subpath
func Test(ctx context.Context, repo, branch, subpath string) (string, error) {
	c, err := dagger.Connect(ctx)
	if err != nil {
		return "", err
	}
	defer c.Close()

	subpath = filepath.Join(".", subpath)
	return baseContainer(c, repo, branch).
		WithExec([]string{"go", "test", "-v", "./" + subpath}).
		Stdout(ctx)
}

func baseContainer(c *dagger.Client, repo, branch string) *dagger.Container {
	if branch == "" {
		branch = "main"
	}
	repoDir := c.Git(repo).Branch(branch).Tree()
	mntPath := "/src"
	return c.Container().
		From("golang:1.20-alpine").
		WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", c.CacheVolume("go-build")).
		WithMountedDirectory(mntPath, repoDir).
		WithWorkdir(mntPath)
}
