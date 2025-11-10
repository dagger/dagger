// The Python SDK's development module.
package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/python-sdk-dev/internal/dagger"
)

var supportedVersions = []string{"3.13", "3.12", "3.11", "3.10"}

// The Python SDK's development module.
type PythonSdkDev struct {
	// Container to run commands in
	Container         *dagger.Container
	SupportedVersions []string
	UvImage           string
	UvVersion         string
}

func New(
	ctx context.Context,
	// Directory with sources
	// +defaultPath="/sdk/python"
	// +ignore=["*", "!*.toml", "!*.lock", "!*/*.toml", "!*/*.lock", "!.python-version", "!dev/src/**/*.py", "!docs/**/*.py", "!docs/**/*.rst", "!runtime/Dockerfile", "!src/**/*.py", "!src/**/py.typed", "!tests/**/*.py", "!codegen/**/*.py", "!README.md", "!LICENSE"]
	source *dagger.Directory,
	// Base container
	// +optional
	container *dagger.Container,
) (*PythonSdkDev, error) {
	uvImage, err := dag.PythonSDK().UvImage(ctx)
	if err != nil {
		return nil, err
	}
	if container == nil {
		container = dag.Wolfi().
			Container(dagger.WolfiContainerOpts{Packages: []string{"libgcc"}}).
			WithEnvVariable("PYTHONUNBUFFERED", "1").
			WithEnvVariable(
				"PATH",
				"/root/.local/bin:/usr/local/bin:$PATH",
				dagger.ContainerWithEnvVariableOpts{Expand: true}).
			With(toolsCache("uv", "ruff", "mypy")).
			With(uv(uvImage))
	}
	return &PythonSdkDev{
		Container: container.WithDirectory("/src/sdk/python", source).
			WithWorkdir("/src/sdk/python").
			WithExec([]string{"uv", "sync"}),
		SupportedVersions: supportedVersions,
		UvImage:           uvImage,
	}, nil
}

// Add the uv tool to the container.
func uv(image string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithDirectory(
				"/usr/local/bin",
				dag.Container().From(image).Rootfs(),
				dagger.ContainerWithDirectoryOpts{Include: []string{"uv*"}}).
			WithEnvVariable("UV_LINK_MODE", "copy").
			WithEnvVariable("UV_PROJECT_ENVIRONMENT", "/opt/venv")
	}
}

// Set up the cache directory for multiple tools.
func toolsCache(args ...string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		for _, tool := range args {
			ctr = ctr.
				WithMountedCache(
					fmt.Sprintf("/root/.cache/%s", tool),
					dag.CacheVolume(fmt.Sprintf("modpythondev-%s", tool))).
				WithEnvVariable(
					fmt.Sprintf("%s_CACHE_DIR", strings.ToUpper(tool)),
					fmt.Sprintf("/root/.cache/%s", tool))
		}
		return ctr
	}
}

// Mount a directory on the base container.
func (m *PythonSdkDev) WithDirectory(
	// The directory to add
	source *dagger.Directory,
) *PythonSdkDev {
	m.Container = m.Container.WithDirectory("/src", source)
	return m
}

// Replace container.
func (m *PythonSdkDev) WithContainer(ctr *dagger.Container) *PythonSdkDev {
	m.Container = ctr
	return m
}

// Generate the client bindings for the core API.
func (m *PythonSdkDev) Generate(
	// Result of the introspection query
	introspectionJSON *dagger.File,
) *dagger.Changeset {
	path := "/src/sdk/python/src/dagger/client/gen.py"
	src := m.Container.Directory(".")
	return m.Container.
		WithMountedFile("/schema.json", introspectionJSON).
		WithExec([]string{
			"uv",
			"run",
			"python",
			"-m",
			"codegen",
			"generate",
			"-i",
			"/schema.json",
			"-o",
			path,
		}).
		Directory(".").
		Changes(src)
}

// Run the type checker (mypy).
func (m *PythonSdkDev) Typecheck(ctx context.Context) (string, error) {
	return m.Container.WithExec([]string{"uv", "run", "mypy", "."}).Stdout(ctx)
}

// Check for linting errors.
func (m *PythonSdkDev) Lint(
	ctx context.Context,
	// List of files or directories to check
	// +optional
	paths []string,
) (string, error) {
	if paths == nil {
		paths = []string{}
	}
	return m.Container.
		WithExec(append([]string{"uv", "run", "ruff", "check"}, paths...)).
		WithExec(append([]string{"uv", "run", "ruff", "format", "--check", "--diff"}, paths...)).
		Stdout(ctx)
}

// Format source files.
func (m *PythonSdkDev) Format(
	// List of files or directories to check
	paths []string,
) *dagger.Directory {
	return m.Container.
		WithExec(append([]string{"uv", "run", "ruff", "check", "--fix-only"}, paths...)).
		WithExec(append([]string{"uv", "run", "ruff", "format"}, paths...)).
		Directory("")
}

// Run the test suite.
func (m *PythonSdkDev) Test(
	// Python version to test against
	// +optional
	version string,
	// Disable nested execution for the test runs
	// +optional
	disableNestedExec bool,
) TestSuite {
	return TestSuite{
		Container:         m.Container,
		Version:           version,
		DisableNestedExec: disableNestedExec,
	}
}

// Run the test suite for all supported versions.
func (m *PythonSdkDev) TestVersion() []TestSuite {
	res := make([]TestSuite, len(supportedVersions))
	for i, version := range supportedVersions {
		res[i] = m.Test(version, false)
	}
	return res
}

// Build the Python SDK client library package for distribution.
func (m *PythonSdkDev) Build(
	// The version for the distribution package
	// +default="0.0.0"
	version string,
) *dagger.Container {
	return m.Container.
		WithoutDirectory("dist").
		WithExec([]string{"uv", "version", version}).
		WithExec([]string{"uv", "build"})
}

// Publish Python SDK client library to PyPI.
func (m *PythonSdkDev) Publish(
	// The token for the upload
	token *dagger.Secret,
	// The version for the distribution package to publish
	// +default="0.0.0"
	version string,
	// The URL of the upload endpoint (empty means PyPI)
	// +optional
	url string,
) *dagger.Container {
	ctr := m.Build(version).WithSecretVariable("UV_PUBLISH_TOKEN", token)

	if url != "" {
		ctr = ctr.WithEnvVariable("UV_PUBLISH_URL", url)
	}

	return ctr.WithExec([]string{"uv", "publish"})
}

// Test the publishing of the Python SDK client library to TestPyPI.
func (m *PythonSdkDev) TestPublish(
	// TestPyPI token
	token *dagger.Secret,
	// The version for the distribution package to publish
	// +optional
	version string,
) *dagger.Container {
	return m.Publish(token, version, "https://test.pypi.org/legacy/")
}

// Preview the reference documentation.
func (m *PythonSdkDev) Docs() Docs {
	return Docs{
		Container: m.Container,
	}
}
