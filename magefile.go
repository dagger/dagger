//go:build mage
// +build mage

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/google/go-cmp/cmp"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

type Lint mg.Namespace

// All runs all lint targets
func (t Lint) All(ctx context.Context) error {
	mg.Deps(
		t.Codegen,
		t.Markdown,
	)
	return nil
}

// Markdown lints the markdown files
func (Lint) Markdown(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	workdir := c.Host().Workdir()

	src, err := workdir.ID(ctx)
	if err != nil {
		return err
	}

	cfg, err := workdir.File(".markdownlint.yaml").ID(ctx)
	if err != nil {
		return err
	}

	_, err = c.Container().
		From("tmknom/markdownlint:0.31.1").
		WithMountedDirectory("/src", src).
		WithMountedFile("/src/.markdownlint.yaml", cfg).
		WithWorkdir("/src").
		Exec(dagger.ContainerExecOpts{
			Args: []string{
				"-c",
				".markdownlint.yaml",
				"--",
				"./docs",
				"README.md",
			},
		}).ExitCode(ctx)
	return err
}

// Codegen ensure the SDK code was re-generated
func (Lint) Codegen(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	generated, err := generator.IntrospectAndGenerate(ctx, c, generator.Config{
		Package: "dagger",
	})
	if err != nil {
		return err
	}

	// grab the file from the repo
	src, err := c.
		Host().
		Workdir().
		File("sdk/go/api.gen.go").
		Contents(ctx)
	if err != nil {
		return err
	}

	// compare the two
	diff := cmp.Diff(string(generated), src)
	if diff != "" {
		return fmt.Errorf("generated api mismatch. please run `go generate ./...`:\n%s", diff)
	}

	return nil
}

// Build builds the binary
func Build(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	workdir := c.Host().Workdir()
	builder := c.Container().
		From("golang:1.19-alpine").
		WithEnvVariable("CGO_ENABLED", "0").
		WithEnvVariable("GOOS", runtime.GOOS).
		WithEnvVariable("GOARCH", runtime.GOARCH).
		WithWorkdir("/app")

	// install dependencies
	modules := c.Directory()
	for _, f := range []string{"go.mod", "go.sum", "sdk/go/go.mod", "sdk/go/go.sum"} {
		fileID, err := workdir.File(f).ID(ctx)
		if err != nil {
			return err
		}

		modules = modules.WithFile(f, fileID)
	}
	modID, err := modules.ID(ctx)
	if err != nil {
		return err
	}
	builder = builder.
		WithMountedDirectory("/app", modID).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "mod", "download"},
		})

	src, err := workdir.ID(ctx)
	if err != nil {
		return err
	}

	builder = builder.
		WithMountedDirectory("/app", src).WithWorkdir("/app").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "./bin/cloak", "-ldflags", "-s -w", "/app/cmd/cloak"},
		})

	ok, err := builder.Directory("./bin").Export(ctx, "./bin")
	if err != nil {
		return err
	}

	if !ok {
		return errors.New("HostDirectoryWrite not ok")
	}
	return nil
}
