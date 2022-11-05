package mage

import (
	"context"
	"fmt"
	"os"
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
	sdkHelper      = "dagger-sdk-helper"
	buildkitRepo   = "github.com/moby/buildkit"
	buildkitBranch = "master"
	// TODO: placeholder until real one exists
	// engineImageRef = "localhost:5000/dagger-engine:latest"
	engineImageRef = "eriksipsma/test-dagger:rebase"
)

func (t Engine) Release(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	buildkitRepo := c.Git(buildkitRepo).Branch(buildkitBranch).Tree()

	arches := []string{"amd64", "arm64"}
	oses := []string{"linux", "darwin"}
	var platformVariants []*dagger.Container
	for _, arch := range arches {
		buildkitBase := c.Container(dagger.ContainerOpts{
			Platform: dagger.Platform("linux/" + arch),
		}).Build(buildkitRepo)
		for _, os := range oses {
			// include each helper for each arch too in case there is a
			// client/server mismatch
			for _, arch := range arches {
				helperBin := util.GoBase(c).
					WithEnvVariable("GOOS", os).
					WithEnvVariable("GOARCH", arch).
					Exec(dagger.ContainerExecOpts{
						Args: []string{"go", "build", "-o", "./bin/" + sdkHelper, "-ldflags", "-s -w", "/app/cmd/sdk-helper"},
					}).
					File("./bin/" + sdkHelper)
				buildkitBase = buildkitBase.WithFS(
					buildkitBase.FS().WithFile("/usr/bin/"+sdkHelper+"-"+os+"-"+arch, helperBin),
				)
			}
		}
		platformVariants = append(platformVariants, buildkitBase)
	}

	imageRef, err := c.Container().Publish(ctx, engineImageRef, dagger.ContainerPublishOpts{
		PlatformVariants: platformVariants,
	})
	if err != nil {
		return err
	}
	fmt.Println("Image published:", imageRef)

	return nil
}
