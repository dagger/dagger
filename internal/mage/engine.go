package mage

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/sdk"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"golang.org/x/mod/semver"
)

const (
	EngineImageRef = "ghcr.io/dagger/engine"
)

func taggedEngineImageRef(tag string) (string, error) {
	if tag != "main" {
		if ok := semver.IsValid(tag); !ok {
			return "", fmt.Errorf("invalid semver tag: %s", tag)
		}
	}
	return fmt.Sprintf("%s:%s", EngineImageRef, tag), nil
}

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

func (t Engine) Publish(ctx context.Context, tag string) error {
	engineImageRef, err := taggedEngineImageRef(tag)
	if err != nil {
		return err
	}

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	arches := []string{"amd64", "arm64"}
	oses := []string{"linux", "darwin", "windows"}

	imageRef, err := c.Container().Publish(ctx, engineImageRef, dagger.ContainerPublishOpts{
		PlatformVariants: util.DevEngineContainer(c, arches, oses),
	})
	if err != nil {
		return err
	}

	if semver.IsValid(tag) {
		sdks := sdk.All{}
		if err := sdks.Bump(ctx, imageRef); err != nil {
			return err
		}
	}

	time.Sleep(3 * time.Second) // allow buildkit logs to flush, to minimize potential confusion with interleaving
	fmt.Println("PUBLISHED IMAGE REF:", imageRef)

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

func (t Engine) test(ctx context.Context, race bool) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	cgoEnabledEnv := "0"
	args := []string{"go", "test", "-p", "16", "-v", "-count=1"}
	if race {
		args = append(args, "-race", "-timeout=1h")
		cgoEnabledEnv = "1"
	}
	args = append(args, "./...")

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		output, err := util.GoBase(c).
			WithMountedFile("/usr/bin/cloak", util.DaggerBinary(c)).
			WithMountedFile("/usr/bin/dagger-engine-session", util.EngineSessionBinary(c)).
			WithMountedDirectory("/app", util.Repository(c)). // need all the source for extension tests
			WithWorkdir("/app").
			WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
			WithMountedDirectory("/root/.docker", util.HostDockerDir(c)).
			Exec(dagger.ContainerExecOpts{
				Args:                          args,
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout().Contents(ctx)
		if err != nil {
			return err
		}
		fmt.Println(output)
		return nil
	})
}

func (t Engine) Test(ctx context.Context) error {
	return t.test(ctx, false)
}

func (t Engine) TestRace(ctx context.Context) error {
	return t.test(ctx, true)
}
