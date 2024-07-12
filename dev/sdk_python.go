package main

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dev/internal/dagger"
	"github.com/dagger/dagger/dev/internal/util"
)

// TODO: use dev module (this is just the mage port)

const (
	pythonSubdir           = "sdk/python"
	pythonRuntimeSubdir    = "sdk/python/runtime"
	pythonGeneratedAPIPath = "sdk/python/src/dagger/client/gen.py"
	pythonDefaultVersion   = "3.12"
)

var (
	pythonVersions = []string{"3.10", "3.11", "3.12"}
)

type PythonSDK struct {
	Dagger *DaggerDev // +private
}

// Lint the Python SDK
func (t PythonSDK) Lint(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)

	base := t.pythonBase(pythonDefaultVersion, true)

	eg.Go(func() error {
		path := "docs/current_docs"
		_, err := base.
			WithDirectory(
				fmt.Sprintf("/%s", path),
				t.Dagger.Source().Directory(path),
				dagger.ContainerWithDirectoryOpts{
					Include: []string{
						"**/*.py",
						".ruff.toml",
					},
				},
			).
			WithFile("/.ruff.toml", t.Dagger.Source.File(".ruff.toml")).
			WithExec([]string{"hatch", "fmt", "--linter", "--check", ".", "/docs"}).
			Sync(ctx)
		return err
	})

	eg.Go(func() error {
		return util.DiffDirectoryF(ctx, t.Dagger.Source(), t.Generate, pythonGeneratedAPIPath)
	})

	eg.Go(func() error {
		return dag.
			Go(t.Dagger.WithModCodegen().Source()).
			Lint(ctx, dagger.GoLintOpts{Packages: []string{pythonRuntimeSubdir}})
	})

	return eg.Wait()
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
		WithWorkdir("codegen").
		WithMountedFile("/schema.json", introspection).
		WithExec([]string{
			"uv", "run", "--no-dev",
			"python", "-m",
			"codegen", "generate", "-i", "/schema.json", "-o", "gen.py",
		}).
		WithExec([]string{"hatch", "fmt", "--formatter", "gen.py"}).
		File("gen.py")
	return dag.Directory().WithFile(pythonGeneratedAPIPath, generated), nil
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
	pypiToken *dagger.Secret,
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

// pythonBase returns a python container with the Python SDK source files
// added and dependencies installed.
func (t PythonSDK) pythonBase(version string, install bool) *dagger.Container {
	src := t.Dagger.Source().Directory(pythonSubdir)

	venv := "/opt/venv"

	base := dag.Container().
		From(fmt.Sprintf("python:%s-slim", version)).
		WithEnvVariable("PYTHONUNBUFFERED", "1").
		// TODO: the way uv is installed here is temporary. The PythonSDK
		// object will be refactored soon to use sdk/python/dev as a dependency.
		WithDirectory(
			"/usr/local/bin",
			dag.Directory().
				WithFile("", src.File("runtime/Dockerfile")).
				DockerBuild(dagger.DirectoryDockerBuildOpts{Target: "uv"}).
				Rootfs(),
			dagger.ContainerWithDirectoryOpts{
				Include: []string{"uv*"},
			},
		).
		WithMountedCache("/root/.cache/uv", dag.CacheVolume("modpython-uv")).
		WithEnvVariable("PIPX_BIN_DIR", "/usr/local/bin").
		WithMountedDirectory("/opt/tools", src.Directory("dev/tools")).
		WithMountedCache("/root/.cache/uv", dag.CacheVolume("uv_cache_"+version)).
		WithMountedCache("/root/.cache/hatch", dag.CacheVolume("hatch_cache_"+version)).
		WithExec([]string{"uv", "venv", "/opt/hatch"}).
		WithExec([]string{
			"uv", "pip", "install",
			"--no-deps",
			"-p", "/opt/hatch/bin/python",
			"-r", "/opt/tools/hatch/requirements.lock",
		}).
		WithExec([]string{"ln", "-s", "/opt/hatch/bin/hatch", "/usr/local/bin/hatch"}).
		WithExec([]string{"uv", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable(
			"PATH",
			"$VIRTUAL_ENV/bin:$PATH",
			dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			},
		).
		// Mirror the same dir structure from the repo because of the
		// relative paths in ruff (for docs linting).
		WithWorkdir(pythonSubdir)

	if install {
		base = base.
			WithMountedDirectory("", src).
			WithExec([]string{"uv", "sync", "--preview"})
	}

	return base
}
