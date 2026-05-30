// Toolchain to develop the Dagger Python SDK
package main

import (
	"context"
	_ "embed"
	"fmt"
	"runtime"
	"strings"

	"dagger/python-sdk-dev/internal/dagger"
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

var supportedVersions = []string{"3.14", "3.13", "3.12", "3.11", "3.10"}

// Lint the Python snippets in the documentation
// +check
func (t PythonSdkDev) LintDocsSnippets(
	// +defaultPath="/"
	// +ignore=[
	//  "*",
	//  "!docs/current_docs/**/*.py",
	//  "!**/.ruff.toml"
	// ]
	workspace *dagger.Directory,
) *dagger.Container {
	// Preserve same file hierarchy for docs because of extend rules in .ruff.toml
	return t.WithDirectory(workspace).Lint([]string{"../../docs"})
}

// +check
// Check for linting errors
func (t PythonSdkDev) Lint(
	// List of files or directories to check
	// +default=[]
	paths []string,
) *dagger.Container {
	return t.DevContainer.
		WithExec(append(uvRun("ruff", "check"), paths...)).
		WithExec(append(uvRun("ruff", "format", "--check", "--diff"), paths...))
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

// Test suite for python 3.10
func (t PythonSdkDev) Python310() *TestForPythonVersion {
	return &TestForPythonVersion{
		Container: t.DevContainer,
		Version:   "3.10",
	}
}

// Test suite for python 3.11
func (t PythonSdkDev) Python311() *TestForPythonVersion {
	return &TestForPythonVersion{
		Container: t.DevContainer,
		Version:   "3.11",
	}
}

// Test suite for python 3.12
func (t PythonSdkDev) Python312() *TestForPythonVersion {
	return &TestForPythonVersion{
		Container: t.DevContainer,
		Version:   "3.12",
	}
}

// Test suite for python 3.13
func (t PythonSdkDev) Python313() *TestForPythonVersion {
	return &TestForPythonVersion{
		Container: t.DevContainer,
		Version:   "3.13",
	}
}

// Test suite for python 3.14
func (t PythonSdkDev) Python314() *TestForPythonVersion {
	return &TestForPythonVersion{
		Container: t.DevContainer,
		Version:   "3.14",
	}
}

// Regenerate the core Python client library
// +generate
func (t PythonSdkDev) ClientLibrary(_ context.Context) (*dagger.Changeset, error) {
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

func (t PythonSdkDev) Provision(
	ctx context.Context,
	// Dagger binary to use for test
	cliBin *dagger.File,
	// _EXPERIMENTAL_DAGGER_RUNNER_HOST value
	// +optional
	runnerHost string,
) (*dagger.Container, error) {
	archiveName := fmt.Sprintf("dagger_v0.x.y_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	checksumsName := "checksums.txt"

	httpServer := t.DevContainer.
		WithMountedFile("/src/dagger", cliBin).
		WithWorkdir("/work").
		WithExec([]string{"tar", "cvzf", archiveName, "-C", "/src", "dagger"}).
		WithExec(
			[]string{"sha256sum", archiveName},
			dagger.ContainerWithExecOpts{RedirectStdout: checksumsName}).
		WithExec([]string{"python", "-m", "http.server"}).
		WithExposedPort(8000).
		AsService()

	httpServerURL, err := httpServer.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "http"})
	if err != nil {
		return nil, err
	}
	archiveURL := fmt.Sprintf("%s/%s", httpServerURL, archiveName)
	checksumsURL := fmt.Sprintf("%s/%s", archiveURL, checksumsName)

	dockerVersion := "24.0.7"

	ctr := dag.Dockerd().Attach(
		t.DevContainer.WithMountedFile(
			"/opt/docker.tgz",
			dag.HTTP(fmt.Sprintf("https://download.docker.com/linux/static/stable/%s/docker-%s.tgz", runtime.GOARCH, dockerVersion)),
			dagger.ContainerWithMountedFileOpts{Owner: "root"}).
			WithExec([]string{
				"tar",
				"xzvf",
				"/opt/docker.tgz",
				"--strip-components=1",
				"-C",
				"/usr/local/bin",
				"docker/docker",
			}),
		dagger.DockerdAttachOpts{DockerVersion: dockerVersion})

	if runnerHost != "" {
		ctr = ctr.WithEnvVariable(
			"_EXPERIMENTAL_DAGGER_RUNNER_HOST",
			runnerHost)
	}

	return ctr.
			WithServiceBinding("http_server", httpServer).
			WithEnvVariable("_INTERNAL_DAGGER_TEST_CLI_URL", archiveURL).
			WithEnvVariable("_INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL", checksumsURL).
			WithExec(
				[]string{"pytest", "-m", "provision"},
				dagger.ContainerWithExecOpts{InsecureRootCapabilities: true}),
		nil
}
