// Toolchain to develop the Dagger Python SDK
package main

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"dagger/python-sdk-dev/internal/dagger"

	"github.com/dagger/dagger/util/parallel"
)

// A toolchain to develop the Dagger Python SDK
type PythonSdkDev struct {
	// Python container to develop Python SDK
	DevContainer *dagger.Container
	Workspace    *dagger.Directory
	SourcePath   string
	// Supported Python versions
	SupportedVersions []string
}

func New(
	// A workspace containing the SDK source code and other relevant files
	// +defaultPath="/"
	// +ignore=[
	//   "*",
	//   "!sdk/python/*.toml",
	//   "!sdk/python/*.lock",
	//   "!sdk/python/*/*.toml",
	//   "!sdk/python/*/*.lock",
	//   "!sdk/python/.python-version",
	//   "!sdk/python/dev/src/**/*.py",
	//   "!sdk/python/docs/**/*.py",
	//   "!sdk/python/docs/**/*.rst",
	//   "!sdk/python/runtime/images",
	//   "!sdk/python/src/**/*.py",
	//   "!sdk/python/src/**/py.typed",
	//   "!sdk/python/tests/**/*.py",
	//   "!sdk/python/codegen/**/*.py",
	//   "!sdk/python/README.md",
	//   "!sdk/python/LICENSE"
	// ]
	workspace *dagger.Directory,

	// +default="sdk/python"
	sourcePath string,
) *PythonSdkDev {
	return &PythonSdkDev{
		DevContainer: dag.DaggerEngine().InstallClient(
			dag.Wolfi().
				Container(dagger.WolfiContainerOpts{Packages: []string{"libgcc"}}).
				WithEnvVariable("PYTHONUNBUFFERED", "1").
				WithEnvVariable(
					"PATH",
					"/root/.local/bin:/usr/local/bin:$PATH",
					dagger.ContainerWithEnvVariableOpts{Expand: true}).
				With(toolsCache("uv", "ruff", "mypy")).
				With(uvTool(workspace)).
				WithDirectory("/src/sdk/python", workspace.Directory(sourcePath)).
				WithWorkdir("/src/sdk/python").
				WithExec(uv("sync"))),
		Workspace:         workspace,
		SourcePath:        sourcePath,
		SupportedVersions: supportedVersions,
	}
}

var supportedVersions = []string{"3.13", "3.12", "3.11", "3.10"}

// Lint the Python snippets in the documentation
// +check
func (t PythonSdkDev) LintDocsSnippets(
	ctx context.Context,
	// +defaultPath="/"
	// +ignore=[
	//  "*",
	//  "!docs/current_docs/**/*.py",
	//  "!**/.ruff.toml"
	// ]
	workspace *dagger.Directory,
) error {
	// Preserve same file hierarchy for docs because of extend rules in .ruff.toml
	return t.WithDirectory(workspace).Lint(ctx, []string{"../.."})
}

// +check
// Check for linting errors
func (t PythonSdkDev) Lint(
	ctx context.Context,
	// List of files or directories to check
	// +default=[]
	paths []string,
) error {
	_, err := t.DevContainer.
		WithExec(append(uvRun("ruff", "check"), paths...)).
		WithExec(append(uvRun("ruff", "format", "--check", "--diff"), paths...)).
		Sync(ctx)
	return err
}

// +check
// Format source files
func (t PythonSdkDev) Format(
	// List of files or directories to check
	// +default=[]
	paths []string,
) *dagger.Changeset {
	before := t.DevContainer.Directory("/src")
	return t.DevContainer.
		WithExec(append(uvRun("ruff", "check", "--fix-only"), paths...)).
		WithExec(append(uvRun("ruff", "format"), paths...)).
		Directory("/src").
		Changes(before)
}

// Run the type checker (mypy)
// FIXME: this is not included as an automated check. Should it?
func (t PythonSdkDev) Typecheck(ctx context.Context) error {
	_, err := t.DevContainer.
		WithExec(uvRun("mypy", ".")).
		Sync(ctx)
	return err
}

// Mount a directory on the base container
// +private
func (t PythonSdkDev) WithDirectory(
	// The directory to add
	source *dagger.Directory,
) PythonSdkDev {
	t.DevContainer = t.DevContainer.WithDirectory("/src", source)
	return t
}

// +check
// Test the Python SDK
func (t PythonSdkDev) Test(ctx context.Context) error {
	// FIXME: apply Erik's nested fix fix 2025-nov-7
	jobs := parallel.New()
	for _, version := range supportedVersions {
		jobs = jobs.WithJob("test with python version "+version, func(ctx context.Context) error {
			_, err := t.TestSuite(version, false).
				Default().
				Sync(ctx)
			return err
		})
	}
	return jobs.Run(ctx)
}

// TestSuite to run unit and other tests
func (t PythonSdkDev) TestSuite(
	// Python version
	// +optional
	version string,
	// Disable nested execution for the test runs
	// +optional
	disableNestedExec bool,
) *TestSuite {
	return &TestSuite{
		Container:         t.DevContainer,
		Version:           version,
		DisableNestedExec: disableNestedExec,
	}
}

// Regenerate the core Python client library
func (t PythonSdkDev) Generate(_ context.Context) (*dagger.Changeset, error) {
	devContainer := t.DevContainer

	// We don't control the input source, it's defined in wrapped native module
	srcMountPath := "/src"
	src := devContainer.Directory(srcMountPath)
	// FIXME: workaround for Directory.changes() bug
	src = dag.Directory().WithDirectory("", src)
	genFile := devContainer.
		WithMountedFile("/schema.json", dag.DaggerEngine().IntrospectionJSON()).
		WithWorkdir("codegen").
		WithExec(uvRun(
			"python", "-m", "codegen", "generate", "-i", "/schema.json", "-o", "gen.py",
		)).
		WithExec(uvRun(
			"ruff", "check", "--fix-only", "gen.py",
		)).
		WithExec(uvRun(
			"ruff", "format", "gen.py",
		)).
		File("gen.py")
	genRelPath := "src/dagger/client/gen.py"
	formattedGenFile := devContainer.
		WithFile(genRelPath, genFile).
		WithExec(uvRun(
			"ruff", "check", "--fix-only", genRelPath,
		)).
		WithExec(uvRun(
			"ruff", "format", genRelPath,
		)).
		File(genRelPath)
	return changes(
		src,
		src.WithFile("sdk/python/"+genRelPath, formattedGenFile),
		[]string{
			"sdk/python/.uv_cache",
			"sdk/python/.venv",
			"sdk/python/__pycache__",
			"sdk/python/uv.lock",
			"sdk/python/**/__pycache__",
		},
	), nil
}

// Test the publishing process
// +check
func (t PythonSdkDev) ReleaseDryRun(ctx context.Context) error {
	return t.Release(
		ctx,
		"HEAD", // sourceTag
		true,   // dryRun
		"",     // pypiRepo
		nil,    // pypiToken
	)
}

// Release the Python SDK
func (t PythonSdkDev) Release(
	ctx context.Context,

	// Git tag to release from
	sourceTag string,

	// +optional
	dryRun bool,

	// +optional
	pypiRepo string,

	// +optional
	pypiToken *dagger.Secret,
) error {
	version := strings.TrimPrefix(sourceTag, "sdk/python/")

	var ctr *dagger.Container
	if dryRun {
		ctr = t.Build("0.0.0") // no default arg in Go, without self call just replicate the default value
	} else {
		var url string
		if pypiRepo == "test" {
			url = "https://test.pypi.org/legacy/"
		}
		ctr = t.Publish(pypiToken, strings.TrimPrefix(version, "v"), url)
	}
	_, err := ctr.Sync(ctx)
	if err != nil {
		return err
	}

	return nil
}

// Bump the Python SDK's Engine dependency
func (t PythonSdkDev) Bump(_ context.Context, version string) (*dagger.Changeset, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")
	engineReference := fmt.Sprintf("# Code generated by dagger. DO NOT EDIT.\n\nCLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	layer := t.Workspace.WithNewFile("sdk/python/src/dagger/_engine/_version.py", engineReference)
	return layer.Changes(t.Workspace), nil
}

// Build the Python SDK client library package for distribution
func (t PythonSdkDev) Build(
	// The version for the distribution package
	// +default="0.0.0"
	version string,
) *dagger.Container {
	return t.DevContainer.
		WithoutDirectory("dist").
		WithExec(uv("version", version)).
		WithExec(uv("build"))
}

// Publish Python SDK client library to PyPI
func (t PythonSdkDev) Publish(
	// The token for the upload
	token *dagger.Secret,
	// The version for the distribution package to publish
	// +default="0.0.0"
	version string,
	// The URL of the upload endpoint (empty means PyPI)
	// +optional
	url string,
) *dagger.Container {
	ctr := t.Build(version).WithSecretVariable("UV_PUBLISH_TOKEN", token)
	if url != "" {
		ctr = ctr.WithEnvVariable("UV_PUBLISH_URL", url)
	}
	return ctr.WithExec(uv("publish"))
}

// Test the publishing of the Python SDK client library to TestPyPI
func (t PythonSdkDev) TestPublish(
	// TestPyPI token
	token *dagger.Secret,
	// The version for the distribution package to publish
	// +default="0.0.0"
	version string,
) *dagger.Container {
	return t.Publish(token, version, "https://test.pypi.org/legacy/")
}

// Preview the reference documentation
func (t PythonSdkDev) Docs() *Docs {
	return &Docs{
		Container: t.DevContainer,
	}
}
