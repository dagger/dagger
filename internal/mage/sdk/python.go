package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

var (
	pythonGeneratedAPIPaths = []string{
		"sdk/python/src/dagger/api/gen.py",
		"sdk/python/src/dagger/api/gen_sync.py",
	}
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
			Args: []string{"poe", "lint"},
		}).
		ExitCode(ctx)
	if err != nil {
		return err
	}

	return lintGeneratedCode(func() error {
		return t.Generate(ctx)
	}, pythonGeneratedAPIPaths...)
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
			Exec(dagger.ContainerExecOpts{
				Args:                          []string{"poe", "test"},
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
		generated := pythonBase(c).
			Exec(dagger.ContainerExecOpts{
				Args:                          []string{"poe", "generate"},
				ExperimentalPrivilegedNesting: true,
			})

		for _, f := range pythonGeneratedAPIPaths {
			contents, err := generated.File(strings.TrimPrefix(f, "sdk/python/")).Contents(ctx)
			if err != nil {
				return err
			}
			if err := os.WriteFile(f, []byte(contents), 0600); err != nil {
				return err
			}
		}
		return nil
	})
}

// Publish publishes the Python SDK
func (t Python) Publish(ctx context.Context, tag string) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	var (
		version = strings.TrimPrefix(tag, "sdk/python/v")
		token   = os.Getenv("PYPI_TOKEN")
		repo    = os.Getenv("PYPI_REPO")
	)

	if token == "" {
		return errors.New("PYPI_TOKEN environment variable must be set")
	}

	build := pythonBase(c).
		WithEnvVariable("POETRY_DYNAMIC_VERSIONING_BYPASS", version).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"poetry-dynamic-versioning"},
		}).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"poetry", "build"},
		})

	args := []string{"poetry", "publish"}

	if repo == "test" {
		build = build.WithEnvVariable("POETRY_REPOSITORIES_TEST_URL", "https://test.pypi.org/legacy/")
		args = append(args, "-r", "test")
	} else {
		repo = "pypi"
	}

	_, err = build.
		WithEnvVariable(fmt.Sprintf("POETRY_PYPI_TOKEN_%s", strings.ToUpper(repo)), token).
		Exec(dagger.ContainerExecOpts{Args: args}).
		ExitCode(ctx)

	return err
}

func pythonBase(c *dagger.Client) *dagger.Container {
	src := c.Directory().WithDirectory("/", util.Repository(c).Directory("sdk/python"))

	base := c.Container().From("python:3.10-alpine").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"apk", "add", "-U", "--no-cache", "gcc", "musl-dev", "libffi-dev"},
		})

	var (
		path = "/root/.local/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		venv = "/opt/venv"
	)

	base = base.
		WithEnvVariable("PATH", path).
		WithEnvVariable("PIP_NO_CACHE_DIR", "off").
		WithEnvVariable("PIP_DISABLE_PIP_VERSION_CHECK", "on").
		WithEnvVariable("PIP_DEFAULT_TIMEOUT", "100").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"pip", "install", "--user", "poetry==1.2.2", "poetry-dynamic-versioning"},
		}).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"python", "-m", "venv", venv},
		}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable("PATH", fmt.Sprintf("%s/bin:%s", venv, path)).
		WithEnvVariable("POETRY_VIRTUALENVS_CREATE", "false")

	return base.
		WithMountedDirectory("/app", src).
		WithWorkdir("/app").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"poetry", "install"},
		})
}
