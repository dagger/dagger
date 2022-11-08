package mage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
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

	build := util.GoBase(c).
		WithEnvVariable("GOOS", runtime.GOOS).
		WithEnvVariable("GOARCH", runtime.GOARCH).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "./bin/cloak", "-ldflags", "-s -w", "/app/cmd/cloak"},
		})

	_, err = build.Directory("./bin").Export(ctx, "./bin")
	return err
}

// Lint lints the engine
func (t Engine) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	_, err = c.Container().
		From("golangci/golangci-lint:v1.48").
		WithMountedDirectory("/app", util.RepositoryGoCodeOnly(c)).
		WithWorkdir("/app").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"golangci-lint", "run", "-v", "--timeout", "5m"},
		}).ExitCode(ctx)
	return err
}

const (
	// TODO: placeholder until real one exists
	engineImageRef = "ghcr.io/dagger/engine:test"
)

func (t Engine) Publish(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	arches := []string{"amd64", "arm64"}
	oses := []string{"linux", "darwin"}

	imageRef, err := c.Container().Publish(ctx, engineImageRef, dagger.ContainerPublishOpts{
		PlatformVariants: util.DevEngineContainer(c, arches, oses),
	})
	if err != nil {
		return err
	}
	fmt.Println("Image published:", imageRef)

	return nil
}

func (t Engine) Dev(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	containerName, err := util.DevEngine(ctx, c)
	if err != nil {
		return err
	}

	fmt.Println("export DAGGER_HOST=docker-container://" + containerName)
	return nil
}

func (t Engine) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	containerName, err := util.DevEngine(ctx, c)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "go", "test", "-v", "-count=1", "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "DAGGER_HOST=docker-container://"+containerName)
	return cmd.Run()
}
