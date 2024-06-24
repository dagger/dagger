package main

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/internal/dagger"
)

// TODO: use dev module (this is just the mage port)

const (
	pythonSubdir           = "sdk/python"
	pythonRuntimeSubdir    = "sdk/python/runtime"
	pythonGeneratedAPIPath = "sdk/python/src/dagger/client/gen.py"
	pythonDefaultVersion   = "3.11"
)

var (
	pythonVersions = []string{"3.10", "3.11", "3.12"}
)

type PythonSDK struct {
	Dagger *Dagger // +private
}

// Lint the Python SDK, and return an error in case of issue
func (t PythonSDK) Lint(ctx context.Context) error {
	report, err := t.LintReport(ctx)
	if err != nil {
		return err
	}
	return report.AssertPass(ctx)
}

// Produce a lint report for the Python SDK
// FIXME: rename this to Lint soon, it's a better interface
func (t PythonSDK) LintReport(ctx context.Context) (*LintReport, error) {
	goSource := t.Dagger.Source.Directory("sdk/python/runtime")
	pySource := dag.Directory().WithDirectory(
		"/",
		t.Dagger.Source,
		DirectoryWithDirectoryOpts{Include: []string{
			"**/*.py",
			"**/.ruff.toml",
			"**/pyproject.toml",
		}},
	)
	return t.lintReport(ctx, goSource, pySource)
}

// Produce a lint report for the Python SDK
// This is a private implementation because it simulates future support
// for context directories, which makes its API cleaner.
// FIXME: when context directories ship, make this public
func (t PythonSDK) lintReport(
	ctx context.Context,
	// Source code of the Python runtime (written in Go)
	// +default="/sdk/python/runtime"
	goSource *dagger.Directory,

	// Python source code across SDK and docs
	// +default="/"
	// +ignore=["*", "!**/*.py", "!**/.ruff.toml", "!**/pyproject.toml"]
	pySource *dagger.Directory,
) (*LintReport, error) {
	report := new(LintReport)
	eg, ctx := errgroup.WithContext(ctx)
	ctx, span := Tracer().Start(ctx, "lint the Dagger Python SDK")
	defer span.End()
	// Lint the python source
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "lint the python code (client library + associated tooling + docs snippets)")
		defer span.End()
		pyReport, err := new(PythonLint).Lint(ctx, pySource)
		if err != nil {
			return err
		}
		return report.merge(pyReport)
	})
	// Check that core client library (generated) is up-to-date
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "Check that generated client library is up-to-date")
		defer span.End()
		codegenReport, err := t.CheckGenerated(ctx)
		if err != nil {
			return err
		}
		return report.merge(codegenReport)
	})
	// Lint the code of the Python runtime (which is written in Go)
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "Lint the python runtime (which is written in Go)")
		defer span.End()
		goReport, err := new(GoLint).Lint(ctx, goSource.AsModule().GeneratedContextDirectory())
		if err != nil {
			return err
		}
		return report.merge(goReport)
	})
	return report, eg.Wait()
}

// Test the Python SDK
func (t PythonSDK) Test(ctx context.Context) error {
	installer, err := t.Dagger.installer(ctx, "sdk-python-test")
	if err != nil {
		return err
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, version := range pythonVersions {
		base := t.pythonBase(version, true).With(installer)

		eg.Go(func() error {
			_, err := base.
				WithEnvVariable("PYTHONUNBUFFERED", "1").
				WithExec([]string{"pytest", "-Wd", "--exitfirst", "-m", "not provision"}).
				Sync(ctx)
			return err
		})

		// Test build
		dist := t.pythonBase(version, false).
			WithMountedDirectory(
				"/dist",
				base.
					WithExec([]string{"hatch", "build", "--clean"}).
					Directory("dist"),
			)

		for _, ext := range map[string]string{"sdist": "tar.gz", "bdist": "whl"} {
			ext := ext
			eg.Go(func() error {
				_, err := dist.
					WithExec([]string{"sh", "-c", "pip install /dist/*" + ext}).
					WithExec([]string{"python", "-c", "import dagger"}).
					Sync(ctx)
				return err
			})
		}
	}

	return eg.Wait()
}

// Regenerate the Python SDK API
func (t PythonSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk-python-generate")
	if err != nil {
		return nil, err
	}
	introspection, err := t.Dagger.introspection(ctx, installer)
	if err != nil {
		return nil, err
	}
	generated := t.pythonBase(pythonDefaultVersion, true).
		// codegen lock file has a relative `-e .` path
		WithWorkdir("./codegen").
		WithExec([]string{"pip", "install", "-r", "requirements.lock"}).
		WithMountedFile("/schema.json", introspection).
		WithExec([]string{"python", "-m", "codegen", "generate", "-i", "/schema.json", "-o", "gen.py"}).
		WithExec([]string{"black", "gen.py"}).
		File("gen.py")
	return dag.Directory().WithFile(pythonGeneratedAPIPath, generated), nil
}

// Check whether the generated python client library is up-to-date
func (t PythonSDK) CheckGenerated(ctx context.Context) (*LintReport, error) {
	before := dag.
		Directory().
		WithDirectory(
			"/",
			t.Dagger.Source,
			dagger.DirectoryWithDirectoryOpts{Include: []string{pythonGeneratedAPIPath}},
		)
	after, err := t.Generate(ctx)
	if err != nil {
		return nil, err
	}
	diff, err := dag.Dirdiff().DiffRaw(ctx, before, after)
	if err != nil {
		return nil, err
	}
	report := new(LintReport)
	if len(diff) > 0 {
		report.Issues = append(report.Issues, LintIssue{
			Text:    pythonGeneratedAPIPath + ": generated python client is not up-to-date",
			IsError: true,
			Tool:    "PythonSDK.checkGenerated",
		})
	}
	return report, nil
}

// Publish the Python SDK
func (t PythonSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,

	// +optional
	pypiRepo string,
	// +optional
	pypiToken *Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/python/v")
	if dryRun {
		version = "0.0.0"
	}
	if pypiRepo == "" || pypiRepo == "pypi" {
		pypiRepo = "main"
	}

	result := t.pythonBase(pythonDefaultVersion, true).
		WithEnvVariable("SETUPTOOLS_SCM_PRETEND_VERSION", version).
		WithEnvVariable("HATCH_INDEX_REPO", pypiRepo).
		WithEnvVariable("HATCH_INDEX_USER", "__token__").
		WithExec([]string{"hatch", "build"})
	if !dryRun {
		result = result.
			WithSecretVariable("HATCH_INDEX_AUTH", pypiToken).
			WithExec([]string{"hatch", "publish"})
	}
	_, err := result.Sync(ctx)
	return err
}

// Bump the Python SDK's Engine dependency
func (t PythonSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")
	engineReference := fmt.Sprintf("# Code generated by dagger. DO NOT EDIT.\n\nCLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	return dag.Directory().WithNewFile("sdk/python/src/dagger/_engine/_version.py", engineReference), nil
}

// Build a container
// returns a python container with the Python SDK source files
// added and dependencies installed.
func (t PythonSDK) Base(version string, install bool) *Container {
	return t.pythonBase(version, install)
}

// pythonBase returns a python container with the Python SDK source files
// added and dependencies installed.
func (t PythonSDK) pythonBase(version string, install bool) *Container {
	src := t.Dagger.Source.Directory(pythonSubdir)

	pipx := dag.HTTP("https://github.com/pypa/pipx/releases/download/1.2.0/pipx.pyz")
	venv := "/opt/venv"

	base := dag.Container().
		From(fmt.Sprintf("python:%s-slim", version)).
		WithEnvVariable("PIPX_BIN_DIR", "/usr/local/bin").
		WithMountedCache("/root/.cache/pip", dag.CacheVolume("pip_cache_"+version)).
		WithMountedCache("/root/.local/pipx/cache", dag.CacheVolume("pipx_cache_"+version)).
		WithMountedCache("/root/.cache/hatch", dag.CacheVolume("hatch_cache_"+version)).
		WithMountedFile("/pipx.pyz", pipx).
		WithExec([]string{"python", "/pipx.pyz", "install", "hatch==1.12.0"}).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable(
			"PATH",
			"$VIRTUAL_ENV/bin:$PATH",
			dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			},
		).
		WithEnvVariable("HATCH_ENV_TYPE_VIRTUAL_PATH", venv).
		// Mirror the same dir structure from the repo because of the
		// relative paths in ruff (for docs linting).
		WithWorkdir(pythonSubdir).
		WithMountedFile("requirements.txt", src.File("requirements.txt")).
		WithExec([]string{"pip", "install", "-r", "requirements.txt"})

	if install {
		base = base.
			WithMountedDirectory("", src).
			WithExec([]string{"pip", "install", "--no-deps", "."})
	}

	return base
}
