package sdk

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dagger/dagger/internal/mage/util"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"
	"golang.org/x/sync/errgroup"
)

var (
	pythonGeneratedAPIPath = "sdk/python/src/dagger/client/gen.py"
	pythonDefaultVersion   = "3.11"
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

	c = c.Pipeline("sdk").Pipeline("python").Pipeline("lint")

	eg, gctx := errgroup.WithContext(ctx)

	base := pythonBase(c, pythonDefaultVersion)

	eg.Go(func() error {
		path := "docs/current_docs"
		_, err = base.
			WithDirectory(
				fmt.Sprintf("/%s", path),
				util.Repository(c).Directory(path),
				dagger.ContainerWithDirectoryOpts{
					Include: []string{
						"**/*.py",
						".ruff.toml",
					},
				},
			).
			WithExec([]string{"ruff", "check", "--diff", ".", "../../docs/current_docs"}).
			WithExec([]string{"black", "--check", "--diff", ".", "../../docs/current_docs"}).
			Sync(gctx)
		return err
	})

	eg.Go(func() error {
		return util.LintGeneratedCode("sdk:python:generate", func() error {
			return t.Generate(gctx)
		}, pythonGeneratedAPIPath)
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

	c = c.Pipeline("sdk").Pipeline("python").Pipeline("test")

	versions := []string{"3.10", "3.11"}

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(ctx, c.Pipeline("dev-engine"), util.DevEngineOpts{Name: "sdk-python-test"})
	if err != nil {
		return err
	}

	cliBinary, err := util.DevelDaggerBinary(ctx, c)
	if err != nil {
		return err
	}
	cliBinPath := "/.dagger-cli"

	eg, gctx := errgroup.WithContext(ctx)
	for _, version := range versions {
		version := version
		c := c.Pipeline(version)
		base := pythonBase(c, version)

		eg.Go(func() error {
			_, err := base.
				WithServiceBinding("dagger-engine", devEngine).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
				WithMountedFile(cliBinPath, cliBinary).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
				WithExec([]string{"pytest", "-Wd", "--exitfirst"}).
				Sync(gctx)
			return err
		})

		//  Test build
		dist := pythonBaseEnv(c, version).
			Pipeline("build").
			WithMountedDirectory(
				"/dist",
				base.Pipeline("build").
					WithExec([]string{"hatch", "build", "--clean"}).
					Directory("dist"),
			)

		for name, ext := range map[string]string{"sdist": "tar.gz", "bdist": "whl"} {
			name := name
			ext := ext
			eg.Go(func() error {
				_, err := dist.Pipeline(name).
					WithExec([]string{"sh", "-c", "pip install /dist/*" + ext}).
					WithExec([]string{"python", "-c", "import dagger"}).
					Sync(gctx)
				return err
			})
		}
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

	c = c.Pipeline("sdk").Pipeline("python").Pipeline("generate")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(ctx, c.Pipeline("dev-engine"), util.DevEngineOpts{Name: "sdk-python-generate"})
	if err != nil {
		return err
	}

	cliBinary, err := util.DevelDaggerBinary(ctx, c)
	if err != nil {
		return err
	}
	cliBinPath := "/.dagger-cli"

	generated, err := pythonBase(c, pythonDefaultVersion).
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithWorkdir("/").
		WithExec([]string{cliBinPath, "run", "python", "-m", "dagger", "codegen", "-o", pythonGeneratedAPIPath}).
		WithExec([]string{"black", pythonGeneratedAPIPath}).
		File(pythonGeneratedAPIPath).
		Contents(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(pythonGeneratedAPIPath, []byte(generated), 0o600)
}

// Publish publishes the Python SDK
func (t Python) Publish(ctx context.Context, tag string) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("python").Pipeline("publish")

	dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN"))

	var (
		version = strings.TrimPrefix(tag, "sdk/python/v")
		repo    = os.Getenv("PYPI_REPO")
	)

	if repo == "" || repo == "pypi" {
		repo = "main"
	}

	result := pythonBase(c, pythonDefaultVersion).
		WithEnvVariable("SETUPTOOLS_SCM_PRETEND_VERSION", version).
		WithEnvVariable("HATCH_INDEX_REPO", repo).
		WithEnvVariable("HATCH_INDEX_USER", "__token__").
		WithExec([]string{"hatch", "build"})
	if !dryRun {
		token := util.GetHostEnv("PYPI_TOKEN")
		result = result.
			WithSecretVariable("HATCH_INDEX_AUTH", c.SetSecret("pypiToken", token)).
			WithExec([]string{"hatch", "publish"})
	}
	_, err = result.Sync(ctx)
	return err
}

// Bump the Python SDK's Engine dependency
func (t Python) Bump(_ context.Context, version string) error {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")
	engineReference := fmt.Sprintf("# Code generated by dagger. DO NOT EDIT.\n\nCLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	return os.WriteFile("sdk/python/src/dagger/_engine/_version.py", []byte(engineReference), 0o600)
}

// pythonBaseEnv returns a general python environment, without source files.
func pythonBaseEnv(c *dagger.Client, version string) *dagger.Container {
	pipx := c.HTTP("https://github.com/pypa/pipx/releases/download/1.2.0/pipx.pyz")
	venv := "/opt/venv"

	return c.Container().
		From(fmt.Sprintf("python:%s-slim", version)).
		WithEnvVariable("PIPX_BIN_DIR", "/usr/local/bin").
		WithMountedCache("/root/.cache/pip", c.CacheVolume("pip_cache_"+version)).
		WithMountedCache("/root/.local/pipx/cache", c.CacheVolume("pipx_cache_"+version)).
		WithMountedCache("/root/.cache/hatch", c.CacheVolume("hatch_cache_"+version)).
		WithMountedFile("/pipx.pyz", pipx).
		WithExec([]string{"python", "/pipx.pyz", "install", "hatch==1.7.0"}).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable(
			"PATH",
			"$VIRTUAL_ENV/bin:$PATH",
			dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			},
		).
		WithEnvVariable("HATCH_ENV_TYPE_VIRTUAL_PATH", venv)
}

// pythonBase returns a python container with the Python SDK source files
// added and dependencies installed.
func pythonBase(c *dagger.Client, version string) *dagger.Container {
	var (
		appDir = "sdk/python"
	)

	src := util.Repository(c).Directory(appDir)

	// Mirror the same dir structure from the repo because of the
	// relative paths in ruff (for docs linting).
	mountPath := fmt.Sprintf("/%s", appDir)

	reqPath := fmt.Sprintf("%s/requirements", appDir)
	reqFile := fmt.Sprintf("%s.txt", reqPath)

	return pythonBaseEnv(c, version).
		WithDirectory(reqPath, src.Directory("requirements")).
		WithFile(reqFile, src.File("requirements.txt")).
		WithExec([]string{"pip", "install", "-r", reqFile}).
		WithDirectory(mountPath, src).
		WithWorkdir(mountPath).
		WithExec([]string{"pip", "install", "."})
}
