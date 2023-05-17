package main

import (
	"fmt"
	"strings"

	"dagger.io/dagger"
	"golang.org/x/sync/errgroup"
)

type PythonTargets struct {
	// configuration for targets
	RepoSrcDir *dagger.Directory
	SDKSrcDir  *dagger.Directory
	Base       *dagger.Container
}

// Dagger Python SDK targets
func (s SDKTargets) Python(ctx dagger.Context) (PythonTargets, error) {
	const (
		path          = "/root/.local/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		venv          = "/opt/venv"
		appDir        = "sdk/python"
		pythonVersion = "3.11"
	)
	sdkSrcDir := s.SrcDir.Directory(appDir)

	// We mirror the same dir structure from the repo because of the
	// relative paths in ruff (for docs linting).
	mountPath := fmt.Sprintf("/%s", appDir)

	base := ctx.Client().Container().
		From(fmt.Sprintf("python:%s-alpine", pythonVersion)).
		WithEnvVariable("PATH", path).
		WithExec([]string{"apk", "add", "-U", "--no-cache", "gcc", "musl-dev", "libffi-dev"}).
		WithExec([]string{"pip", "install", "--user", "poetry==1.3.1", "poetry-dynamic-versioning"}).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable("PATH", fmt.Sprintf("%s/bin:%s", venv, path)).
		WithEnvVariable("POETRY_VIRTUALENVS_CREATE", "false").
		WithWorkdir(mountPath)

	// FIXME: Use single `poetry.lock` directly with `poetry install --no-root`
	// 	when able: https://github.com/python-poetry/poetry/issues/1301
	reqFile := fmt.Sprintf("%s/requirements.txt", mountPath)
	requirements := base.
		WithMountedDirectory(mountPath, sdkSrcDir).
		WithExec([]string{
			"poetry", "export",
			"--with", "test,lint,dev",
			"--without-hashes",
			"-o", "requirements.txt",
		}).
		File(reqFile)

	deps := base.
		WithRootfs(base.Rootfs().WithFile(reqFile, requirements)).
		WithExec([]string{"pip", "install", "-r", "requirements.txt"})

	deps = deps.
		WithRootfs(base.Rootfs().WithDirectory(mountPath, sdkSrcDir)).
		WithExec([]string{"poetry", "install", "--without", "docs"})

	return PythonTargets{
		SDKSrcDir:  sdkSrcDir,
		RepoSrcDir: s.SrcDir,
		Base:       deps,
	}, nil
}

// Lint the Dagger Python SDK
func (p PythonTargets) Lint(ctx dagger.Context, foo string) (string, error) {
	// TODO: would be cool to write this in python... need support for mixed
	// languages in single project (or project nesting type thing)

	// TODO: pipeline should be automatically set
	c := ctx.Client().Pipeline("sdk").Pipeline("python").Pipeline("lint")

	eg, gctx := errgroup.WithContext(ctx)

	var poeLintOut string
	eg.Go(func() error {
		var err error
		poeLintOut, err = p.Base.
			WithExec([]string{"poe", "lint"}).
			Stdout(gctx)
		return err
	})

	var poeLintDocsOut string
	eg.Go(func() error {
		path := "docs/current/sdk/python/snippets"
		snippets := c.Directory().
			WithDirectory("/", p.RepoSrcDir.Directory(path))
		var err error
		poeLintDocsOut, err = p.Base.
			WithMountedDirectory(fmt.Sprintf("/%s", path), snippets).
			WithExec([]string{"poe", "lint-docs"}).
			Stdout(gctx)
		return err
	})

	// TODO: test generated code too

	return strings.Join([]string{
		poeLintOut,
		poeLintDocsOut,
	}, "\n"), eg.Wait()
}
