package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dagger/dagger/internal/mage/util"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"
	"golang.org/x/sync/errgroup"
)

var (
	pythonGeneratedAPIPaths = []string{
		"sdk/python/src/dagger/api/gen.py",
		"sdk/python/src/dagger/api/gen_sync.py",
	}
	pythonDefaultVersion = "3.10"
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

	eg, gctx := errgroup.WithContext(ctx)

	base := pythonBase(c, pythonDefaultVersion)

	eg.Go(func() error {
		_, err = base.
			WithExec([]string{"poe", "lint"}).
			ExitCode(gctx)
		return err
	})

	eg.Go(func() error {
		workdir := util.Repository(c)
		snippets := c.Directory().
			WithDirectory("/", workdir.Directory("docs/current/sdk/python/snippets"))
		_, err = base.
			WithMountedDirectory("/snippets", snippets).
			WithExec([]string{"poe", "lint", "/snippets"}).
			ExitCode(gctx)
		return err
	})

	eg.Go(func() error {
		return lintGeneratedCode(func() error {
			return t.Generate(ctx)
		}, pythonGeneratedAPIPaths...)
	})

	return eg.Wait()
}

// Test tests the Python SDK
func (t Python) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	versions := []string{"3.10", "3.11"}

	eg, gctx := errgroup.WithContext(ctx)
	for _, version := range versions {
		version := version
		eg.Go(func() error {
			_, err := pythonBase(c, version).
				WithMountedDirectory("/root/.docker", util.HostDockerDir(c)).
				WithExec([]string{"poe", "test", "--exitfirst", "-m", "not provision"}).
				ExitCode(gctx)
			return err
		})
	}

	return eg.Wait()
}

// Generate re-generates the SDK API
func (t Python) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	generated := pythonBase(c, pythonDefaultVersion).
		WithExec([]string{"poe", "generate"})

	for _, f := range pythonGeneratedAPIPaths {
		contents, err := generated.File(strings.TrimPrefix(f, "sdk/python/")).Contents(ctx)
		if err != nil {
			return err
		}
		if err := os.WriteFile(f, []byte(contents), 0o600); err != nil {
			return err
		}
	}
	return nil
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

	build := pythonBase(c, pythonDefaultVersion).
		WithEnvVariable("POETRY_DYNAMIC_VERSIONING_BYPASS", version).
		WithExec([]string{"poetry-dynamic-versioning"}).
		WithExec([]string{"poetry", "build"})

	args := []string{"poetry", "publish"}

	if repo == "test" {
		build = build.WithEnvVariable("POETRY_REPOSITORIES_TEST_URL", "https://test.pypi.org/legacy/")
		args = append(args, "-r", "test")
	} else {
		repo = "pypi"
	}

	_, err = build.
		WithEnvVariable(fmt.Sprintf("POETRY_PYPI_TOKEN_%s", strings.ToUpper(repo)), token).
		WithExec(args).
		ExitCode(ctx)

	return err
}

// Bump the Python SDK's Engine dependency
func (t Python) Bump(ctx context.Context, version string) error {
	engineReference := fmt.Sprintf(`# Code generated by dagger. DO NOT EDIT.
# flake8: noqa

CLI_VERSION = %q
`, version)

	return os.WriteFile("sdk/python/src/dagger/_version.py", []byte(engineReference), 0o600)
}

func pythonBase(c *dagger.Client, version string) *dagger.Container {
	src := c.Directory().WithDirectory("/", util.Repository(c).Directory("sdk/python"))

	var (
		path = "/root/.local/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		venv = "/opt/venv"
	)

	base := c.Container().
		From(fmt.Sprintf("python:%s-alpine", version)).
		WithEnvVariable("PATH", path).
		WithExec([]string{"apk", "add", "-U", "--no-cache", "gcc", "musl-dev", "libffi-dev"}).
		WithExec([]string{"pip", "install", "--user", "poetry==1.2.2", "poetry-dynamic-versioning"}).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable("PATH", fmt.Sprintf("%s/bin:%s", venv, path)).
		WithEnvVariable("POETRY_VIRTUALENVS_CREATE", "false").
		WithWorkdir("/app")

	// FIXME: Use single `poetry.lock` directly with `poetry install --no-root`
	// 	when able: https://github.com/python-poetry/poetry/issues/1301
	requirements := base.
		WithMountedDirectory("/app", src).
		WithExec([]string{
			"poetry", "export",
			"--with", "test,lint,dev",
			"--without-hashes",
			"-o", "requirements.txt",
		}).
		File("/app/requirements.txt")

	deps := base.
		WithRootfs(base.Rootfs().WithFile("/app/requirements.txt", requirements)).
		WithExec([]string{"pip", "install", "-r", "requirements.txt"})

	deps = deps.
		WithRootfs(deps.Rootfs().WithDirectory("/app", src)).
		WithExec([]string{"poetry", "install", "--without", "docs"})

	return util.WithDevEngine(c, deps)
}
