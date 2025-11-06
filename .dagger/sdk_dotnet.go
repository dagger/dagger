package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type DotnetSDK struct {
	Workspace         *dagger.Directory // +private
	OriginalWorkspace *dagger.Directory // +private
	SourcePath        string            // +private
	IntrospectionJSON *dagger.File      // +private
}

// Develop the Dagger Dotnet SDK (experimental)
func (sdks *SDK) Dotnet(
	// The workspace for developing the Dotnet SDK
	// +defaultPath="/"
	// +ignore=["*", "!sdk/dotnet"]
	workspace *dagger.Directory,
	// The path of the Dotnet SDK in the workspace
	// +default="sdk/dotnet"
	sourcePath string,
) DotnetSDK {
	return DotnetSDK{
		Workspace:         workspace,
		OriginalWorkspace: workspace,
		IntrospectionJSON: sdks.Dagger.introspectionJSON(),
	}
}

func (sdks *SDK) selfCallDotnet() DotnetSDK {
	return sdks.Dotnet(
		// workspace
		sdks.Dagger.Source.Filter(
			dagger.DirectoryFilterOpts{Include: []string{"sdk/dotnet"}},
		),
		// sourcePath
		"sdk/dotnet",
	)
}

// Return the source directory of the Dotnet SDK
func (t DotnetSDK) Source() *dagger.Directory {
	return t.Workspace.Directory(t.SourcePath)
}

// Wrap the "native" dotnet SDK dev module (written in dot net)
func (t DotnetSDK) native() *dagger.DotnetSDKDev {
	return dag.DotnetSDKDev(dagger.DotnetSDKDevOpts{Source: t.Source()})
}

func (t DotnetSDK) Name() string {
	return "dotnet"
}

func (t DotnetSDK) Lint(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, dag.DotnetSDKDev().Lint(ctx)
}

func (t DotnetSDK) Test(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, t.native().Test(ctx, t.IntrospectionJSON)
}

// Install the SDK locally so that it can be imported
// NOTE: this was initially called Generate(), but it doesn't do what our CI
// expects an SDK Generate() function to do.
//
// - Expected: generate client library to be committed and published
// - Actual: generate introspection.json which allows *using* the SDK from a local checkout
//
// Since this SDK at the moment cannot be published or installed, the standard Generate()
// function does not need to exist.
// WARNING: if you rename this to Generate(), it wil break CI because it adds a file to the
// repo (introspection.json) which is git-ignored and therefore not present in a clean checkout
// This is why the same check may not fail locally - you have the gitignored copy.
func (t DotnetSDK) Install() *dagger.Changeset {
	return t.WithInstall().Changes()
}

func (t DotnetSDK) WithInstall() DotnetSDK {
	t.Workspace = t.Workspace.
		WithoutDirectory(t.SourcePath).
		WithDirectory(t.SourcePath, t.native().Generate(t.IntrospectionJSON))
	return t
}

func (t DotnetSDK) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t DotnetSDK) Bump(ctx context.Context, version string) (*dagger.Changeset, error) { //nolint:unparam
	// The SDK has no engine to bump at the moment. So skip it.
	return dag.Directory().Changes(dag.Directory()).Sync(ctx)
}
