package main

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

const (
	pythonSubdir           = "sdk/python"
	pythonRuntimeSubdir    = "sdk/python/runtime"
	pythonGeneratedAPIPath = "sdk/python/src/dagger/client/gen.py"
)

var (
	pythonVersions = []string{"3.10", "3.11", "3.12"}
)

type PythonSDK struct {
	Dagger *DaggerDev // +private
}

// dev instantiates a PythonSDKDev instance, with source directory
// from `sdk/python` subdir.
func (t PythonSDK) dev(opts ...dagger.PythonSDKDevOpts) *dagger.PythonSDKDev {
	return dag.PythonSDKDev(opts...)
}

// directory takes a directory returned by PythonSDKDev which is relative
// to `sdk/python` and returns a new directory relative to the repo's
// root.
func (t PythonSDK) directory(dist *dagger.Directory) *dagger.Directory {
	return dag.Directory().WithDirectory(pythonSubdir, dist)
}

// Lint the Python SDK
func (t PythonSDK) Lint(ctx context.Context) (rerr error) {
	eg, ctx := errgroup.WithContext(ctx)

	// TODO: create function in PythonSDKDev to lint any directory as input
	// but reusing the same linter configuration in the SDK.
	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint Python code in the SDK and docs")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		// Preserve same file hierarchy for docs because of extend rules in .ruff.toml
		_, err := t.dev().
			WithDirectory(
				dag.Directory().
					WithDirectory(
						"",
						t.Dagger.Source(),
						dagger.DirectoryWithDirectoryOpts{
							Include: []string{
								"docs/current_docs/**/*.py",
								"**/.ruff.toml",
							},
						},
					),
			).
			Lint(ctx, dagger.PythonSDKDevLintOpts{Paths: []string{"../.."}})

		return err
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that the generated client library is up-to-date")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		before := t.Dagger.Source()
		after, err := t.Generate(ctx)
		if err != nil {
			return err
		}
		return dag.Dirdiff().AssertEqual(ctx, before, after, []string{pythonGeneratedAPIPath})
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint the python runtime, which is written in Go")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		return dag.
			Go(t.Dagger.WithModCodegen().Source()).
			Lint(ctx, dagger.GoLintOpts{Packages: []string{pythonRuntimeSubdir}})
	})

	return eg.Wait()
}

// Test the Python SDK
func (t PythonSDK) Test(ctx context.Context) (rerr error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return err
	}

	base := t.dev().Container().With(installer)
	dev := t.dev(dagger.PythonSDKDevOpts{Container: base})

	eg, ctx := errgroup.WithContext(ctx)
	for _, version := range pythonVersions {
		eg.Go(func() error {
			_, err := dev.
				Test(dagger.PythonSDKDevTestOpts{Version: version}).
				Default().
				Sync(ctx)
			return err
		})
	}

	return eg.Wait()
}

// Regenerate the Python SDK API
func (t PythonSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return nil, err
	}
	introspection, err := t.Dagger.introspection(ctx, installer)
	if err != nil {
		return nil, err
	}
	return t.directory(t.dev().Generate(introspection)), nil
}

// Test the publishing process
func (t PythonSDK) TestPublish(ctx context.Context, tag string) error {
	return t.Publish(ctx, tag, true, "", nil, "https://github.com/dagger/dagger.git", nil)
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

	// +optional
	// +default="https://github.com/dagger/dagger.git"
	gitRepoSource string,
	// +optional
	githubToken *dagger.Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/python/")
	if dryRun {
		version = "v0.0.0"
	}
	if pypiRepo == "" || pypiRepo == "pypi" {
		pypiRepo = "main"
	}

	// TODO: move this to PythonSDKDev
	result := t.dev().Container().
		WithEnvVariable("SETUPTOOLS_SCM_PRETEND_VERSION", strings.TrimPrefix(version, "v")).
		WithEnvVariable("HATCH_INDEX_REPO", pypiRepo).
		WithEnvVariable("HATCH_INDEX_USER", "__token__").
		WithExec([]string{"uvx", "hatch", "build"})
	if !dryRun {
		result = result.
			WithSecretVariable("HATCH_INDEX_AUTH", pypiToken).
			WithExec([]string{"uvx", "hatch", "publish"})
	}
	_, err := result.Sync(ctx)
	if err != nil {
		return err
	}

	if semver.IsValid(version) {
		if err := sdkGithubRelease(ctx, t.Dagger.Git, sdkGithubReleaseOpts{
			tag:         "sdk/python/" + version,
			target:      tag,
			notes:       sdkChangeNotes(t.Dagger.Src, "sdk/python", version),
			gitRepo:     gitRepoSource,
			githubToken: githubToken,
			dryRun:      dryRun,
		}); err != nil {
			return err
		}
	}

	return nil
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
