package sdk

import (
	"context"
	"errors"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

const (
	pythonGeneratedAPIPath = "sdk/python/src/dagger/api/gen.py"
)

var _ SDK = Python{}

type Python mg.Namespace

// Lint lints the Python SDK
func (t Python) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	_, err = pythonBase(c).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"hatch", "run", "lint:style"},
		}).
		ExitCode(ctx)
	if err != nil {
		return err
	}

	return lintGeneratedCode(pythonGeneratedAPIPath, func() error {
		return t.Generate(ctx)
	})
}

// Test tests the Python SDK
func (t Python) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		_, err = pythonBase(c).
			WithEnvVariable("DAGGER_HOST", "unix:///dagger.sock"). // gets automatically rewritten by shim to be http
			Exec(dagger.ContainerExecOpts{
				Args:                          []string{"hatch", "run", "test"},
				ExperimentalPrivilegedNesting: true,
			}).
			ExitCode(ctx)
		return err
	})
}

// Generate re-generates the SDK API
func (t Python) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		generated, err := pythonBase(c).
			Exec(dagger.ContainerExecOpts{
				Args:                          []string{"hatch", "run", "generate"},
				ExperimentalPrivilegedNesting: true,
			}).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"hatch", "run", "lint:fmt"},
			}).
			File(strings.TrimPrefix(pythonGeneratedAPIPath, "sdk/python/")).
			Contents(ctx)
		if err != nil {
			return err
		}

		return os.WriteFile(pythonGeneratedAPIPath, []byte(generated), 0600)
	})
}

// Publish publishes the Python SDK
func (t Python) Publish(ctx context.Context, tag string) error {
	return errors.New("not implemented")
}

func pythonBase(c *dagger.Client) *dagger.Container {
	src := c.Directory().WithDirectory("/", util.Repository(c).Directory("sdk/python"))

	base := c.Container().From("python:3.10-alpine").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"apk", "add", "-U", "--no-cache", "gcc", "musl-dev", "libffi-dev"},
		})

	base = base.
		WithEnvVariable("PIP_NO_CACHE_DIR", "off").
		WithEnvVariable("PIP_DISABLE_PIP_VERSION_CHECK", "on").
		WithEnvVariable("PIP_DEFAULT_TIMEOUT", "100").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"pip", "install", "hatch"},
		})

	return base.
		WithWorkdir("/app").
		WithMountedDirectory("/app", src).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"hatch", "env", "create"},
		}).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"hatch", "env", "create", "lint"},
		})
}
