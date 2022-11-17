//go:build mage
// +build mage

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
	"github.com/google/go-cmp/cmp"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

type Lint mg.Namespace

// Run all lint targets
func (t Lint) All(ctx context.Context) error {
	mg.Deps(
		t.Codegen,
		t.Markdown,
	)
	return nil
}

// Markdown lint
func (Lint) Markdown(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	workdir := c.Host().Workdir().Read()

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

// Lint SDK code generation
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

type Build mg.Namespace

// Dagger will build the dagger binary
func (Build) Dagger(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	builder := c.Container().
		From("golang:1.18.2-alpine").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"mkdir", "-p", "/app/sdk/go"},
		})

	workdir := c.Host().Workdir()

	fs := builder.FS()
	for _, f := range []string{"go.mod", "go.sum", "sdk/go/go.mod", "sdk/go/go.sum"} {
		fileID, err := workdir.Read().File(f).ID(ctx)
		if err != nil {
			return err
		}

		fs = fs.WithCopiedFile(path.Join("/app", f), fileID)
	}

	modFSID, err := fs.ID(ctx)
	if err != nil {
		return err
	}

	builder = builder.WithFS(modFSID).WithWorkdir("/app")
	builder = builder.Exec(dagger.ContainerExecOpts{
		Args: []string{"go", "mod", "download"},
	})

	src, err := workdir.Read().ID(ctx)
	if err != nil {
		return err
	}

	builder = builder.WithMountedDirectory("/app", src).WithWorkdir("/app")

	builder = builder.Exec(dagger.ContainerExecOpts{
		Args: []string{"mkdir", "/app/build"},
	})

	builder = builder.
		WithEnvVariable("CGO_ENABLED", "0").
		WithEnvVariable("GOOS", runtime.GOOS).
		WithEnvVariable("GOARCH", runtime.GOARCH).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "/app/build/cloak", "-ldflags", "-s -w", "/app/cmd/cloak"},
		})

	daggerBuildDir, err := builder.Directory("./build").ID(ctx)
	if err != nil {
		return err
	}

	ok, err := c.Host().Workdir().Write(ctx, daggerBuildDir, dagger.HostDirectoryWriteOpts{Path: "."})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("HostDirectoryWrite not ok")
	}
	return nil
}
