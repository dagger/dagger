package mage

import (
	"context"
	"errors"
	"os"
	"runtime"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

type Engine mg.Namespace

// Build builds the engine binary
func (t Engine) Build(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	src := c.Host().Workdir()

	// Create a directory containing only `go.{mod,sum}` files.
	goMods := c.Directory()
	for _, f := range []string{"go.mod", "go.sum", "sdk/go/go.mod", "sdk/go/go.sum"} {
		goMods = goMods.WithFile(f, src.File(f))
	}

	build := c.Container().
		From("golang:1.19-alpine").
		WithEnvVariable("CGO_ENABLED", "0").
		WithEnvVariable("GOOS", runtime.GOOS).
		WithEnvVariable("GOARCH", runtime.GOARCH).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithMountedDirectory("/app", goMods).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "mod", "download"},
		}).
		// run `go build` with all source
		WithMountedDirectory("/app", src).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "./bin/cloak", "-ldflags", "-s -w", "/app/cmd/cloak"},
		})

	ok, err := build.Directory("./bin").Export(ctx, "./bin")
	if err != nil {
		return err
	}

	if !ok {
		return errors.New("HostDirectoryWrite not ok")
	}
	return nil
}
