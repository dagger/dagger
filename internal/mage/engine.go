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
	engineImage = "ghcr.io/dagger/engine"
)

func parseRef(tag string) error {
	if tag == "main" {
		return nil
	}
	if ok := semver.IsValid(tag); !ok {
		return fmt.Errorf("invalid semver tag: %s", tag)
	}
	return nil
}

type Engine mg.Namespace

// Build builds the engine binary
func (t Engine) Build(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()
	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		build := util.GoBase(c).
			WithEnvVariable("GOOS", runtime.GOOS).
			WithEnvVariable("GOARCH", runtime.GOARCH).
			WithExec([]string{"go", "build", "-o", "./bin/dagger", "-ldflags", "-s -w", "/app/cmd/dagger"})

		_, err = build.Directory("./bin").Export(ctx, "./bin")
		return err
	})
}

// Lint lints the engine
func (t Engine) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		_, err = c.Container().
			From("golangci/golangci-lint:v1.48").
			WithMountedDirectory("/app", util.RepositoryGoCodeOnly(c)).
			WithWorkdir("/app").
			WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
			ExitCode(ctx)
		return err
	})
}

// Publish builds and pushes Engine OCI image to a container registry
func (t Engine) Publish(ctx context.Context, version string) error {
	if err := parseRef(version); err != nil {
		return err
	}

	ref := fmt.Sprintf("%s:%s", engineImage, version)

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		arches := []string{"amd64", "arm64"}
		oses := []string{"linux", "darwin", "windows"}

		digest, err := c.Container().Publish(ctx, ref, dagger.ContainerPublishOpts{
			PlatformVariants: util.DevEngineContainer(c, arches, oses),
		})
		if err != nil {
			return err
		}

		if semver.IsValid(version) {
			sdks := sdk.All{}
			if err := sdks.Bump(ctx, digest); err != nil {
				return err
			}
		}

		time.Sleep(3 * time.Second) // allow buildkit logs to flush, to minimize potential confusion with interleaving
		fmt.Println("PUBLISHED IMAGE REF:", digest)

		return nil
	})
}

// Dev builds the Engine OCI image and prints setup vars
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
	fmt.Println("export DAGGER_RUNNER_HOST=docker-container://" + containerName)
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
			WithMountedDirectory("/app", util.Repository(c)). // need all the source for extension tests
			WithWorkdir("/app").
			WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
			WithMountedDirectory("/root/.docker", util.HostDockerDir(c)).
			WithExec(args, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		if err != nil {
			return err
		}
		fmt.Println(output)
		return nil
	})
}

// Test runs Engine tests
func (t Engine) Test(ctx context.Context) error {
	return t.test(ctx, false)
}

// TestRace runs Engine tests with go race detector enabled
func (t Engine) TestRace(ctx context.Context) error {
	return t.test(ctx, true)
}
