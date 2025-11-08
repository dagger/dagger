package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/python-sdk-dev/internal/dagger"

	"github.com/dagger/dagger/util/parallel"
)

type PythonSdkDev struct {
	Workspace  *dagger.Directory
	SourcePath string
}

func New(
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
	//   "!sdk/python/runtime/Dockerfile",
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
		Workspace:  workspace,
		SourcePath: sourcePath,
	}
}

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
	_, err := dag.NativeToolchain().
		WithDirectory(workspace).
		Lint(ctx, dagger.NativeToolchainLintOpts{Paths: []string{"../.."}})
	return err
}

// Test the Python SDK
func (t PythonSdkDev) Test(ctx context.Context) error {
	// FIXME: apply Erik's nested fix fix 2025-nov-7
	baseContainer := dag.NativeToolchain().Container()
	baseContainer = dag.DaggerEngine().InstallClient(baseContainer)
	nativeToolchain := dag.NativeToolchain(dagger.NativeToolchainOpts{Container: baseContainer})
	// Technically we don't need the custom base container for this operation,
	//  but better caching it we share the same native toolchain instance?
	versions, err := nativeToolchain.SupportedVersions(ctx)
	if err != nil {
		return err
	}
	jobs := parallel.New()
	for _, version := range versions {
		jobs = jobs.WithJob("test with python version "+version, func(ctx context.Context) error {
			_, err := nativeToolchain.
				Test(dagger.NativeToolchainTestOpts{Version: version}).
				Default().
				Sync(ctx)
			return err
		})
	}
	return jobs.Run(ctx)
}

// Regenerate the Python SDK API
func (t PythonSdkDev) Generate(_ context.Context) (*dagger.Changeset, error) {
	devContainer := dag.NativeToolchain().Container()

	// We don't control the input source, it's defined in wrapped native module
	srcMountPath := "/src"
	src := devContainer.Directory(srcMountPath)
	// FIXME: workaround for Directory.changes() bug
	src = dag.Directory().WithDirectory("", src)
	genFile := devContainer.
		WithMountedFile("/schema.json", dag.DaggerEngine().IntrospectionJSON()).
		WithWorkdir("codegen").
		WithExec([]string{
			"uv", "run", "python", "-m", "codegen",
			"generate", "-i", "/schema.json", "-o", "gen.py",
		}).
		WithExec([]string{
			"uv", "run", "ruff", "check", "--fix-only", "gen.py",
		}).
		WithExec([]string{
			"uv", "run", "ruff", "format", "gen.py",
		}).
		File("gen.py")
	genRelPath := "src/dagger/client/gen.py"
	formattedGenFile := devContainer.
		WithFile(genRelPath, genFile).
		WithExec([]string{
			"uv", "run", "ruff", "check", "--fix-only", genRelPath,
		}).
		WithExec([]string{
			"uv", "run", "ruff", "format", genRelPath,
		}).
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
		ctr = dag.NativeToolchain().Build()
	} else {
		opts := dagger.NativeToolchainPublishOpts{
			Version: strings.TrimPrefix(version, "v"),
		}
		if pypiRepo == "test" {
			opts.URL = "https://test.pypi.org/legacy/"
		}
		ctr = dag.NativeToolchain().Publish(pypiToken, opts)
	}
	_, err := ctr.Sync(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (t PythonSdkDev) VersionFromTag(tag string) string {
	prefix := strings.TrimRight(t.SourcePath, "/") + "/"
	return strings.TrimPrefix(t.SourcePath, prefix)
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
