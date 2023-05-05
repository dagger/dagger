package main

import (
	"path/filepath"

	"dagger.io/dagger"
)

func main() {
	dagger.Serve(
		Build,
		Test,
	)
}

// Build the go binary from the given go repo, branch, and subpath. Silly right now due to lack of host export, but proves concept
func Build(ctx dagger.Context, repo, branch, subpath string) (string, error) {
	binpath := filepath.Join("./bin", filepath.Base(subpath))
	return baseContainer(ctx.Client(), repo, branch).
		WithExec([]string{"go", "build", "-v", "-x", "-o", binpath, "./" + subpath}).
		Stderr(ctx)
}

// Test the go binary from given go repo, branch, and subpath
func Test(ctx dagger.Context, repo, branch, subpath string) (string, error) {
	subpath = filepath.Join(".", subpath)
	return baseContainer(ctx.Client(), repo, branch).
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
