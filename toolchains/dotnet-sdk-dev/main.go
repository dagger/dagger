package main

import (
	"context"
	"dagger/dotnet-sdk-dev/internal/dagger"
)

const dotnetSDKImage = "mcr.microsoft.com/dotnet/sdk:8.0-alpine3.20@sha256:d04ba63aae736552b2b03bf0e63efa46d0c765726c831b401044543319d63219"

type DotnetSdkDev struct {
	OriginalWorkspace *dagger.Directory // +private
	Workspace         *dagger.Directory // +private
	SourcePath        string            // +private
	BaseContainer     *dagger.Container
}

// Develop the Dagger Dotnet SDK (experimental)
func New(
	// A directory with all the files needed to develop the SDK.
	// +defaultPath="/"
	// +ignore=["*", "!sdk/dotnet/sdk/.config", "!sdk/dotnet/sdk/Dagger.sln", "!sdk/dotnet/sdk/Dagger.sln.DotSettings.user", "!sdk/dotnet/sdk/global.json", "!sdk/dotnet/sdk/**/*.cs", "!sdk/dotnet/sdk/**/*.csproj"]
	workspace *dagger.Directory,
	// The path of the SDK in the workspace
	// +default="sdk/dotnet/sdk"
	sourcePath string,
) *DotnetSdkDev {
	baseContainer := dag.
		Container().
		From(dotnetSDKImage).
		WithWorkdir("/src").
		With(func(c *dagger.Container) *dagger.Container {
			return dag.DaggerEngine().InstallClient(c)
		})

	return &DotnetSdkDev{
		Workspace:         workspace,
		OriginalWorkspace: workspace,
		SourcePath:        sourcePath,
		BaseContainer:     baseContainer,
	}
}

// Return the Dotnet SDK workspace mounted in a dev container,
// and working directory set to the SDK source
func (t *DotnetSdkDev) DevContainer() *dagger.Container {
	return t.BaseContainer.
		WithMountedDirectory(".", t.Workspace).
		WithWorkdir(t.SourcePath)
}

// Lint the Dotnet SDK with Csharpier (https://csharpier.com/)
// +check
func (t *DotnetSdkDev) Csharpier(ctx context.Context) error {
	_, err := t.DevContainer().
		WithExec([]string{"dotnet", "tool", "restore"}).
		WithExec([]string{"dotnet", "csharpier", "--check", "."}).
		Sync(ctx)

	return err
}

// Test the Dotnet SDK
// +check
func (t *DotnetSdkDev) Test(ctx context.Context) error {
	_, err := t.DevContainer().
		WithFile("Dagger.SDK/introspection.json", dag.DaggerEngine().IntrospectionJSON()).
		WithExec([]string{"dotnet", "restore"}).
		WithExec([]string{"dotnet", "build", "--no-restore"}).
		WithExec([]string{"dotnet", "test", "--no-build"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Sync(ctx)

	return err
}

// Install the SDK locally so that it can be imported.
//
// This function generate `introspection.json` which allows using the
// SDK from a local checkout.
//
// Since this SDK at the moment cannot be published or installed, the standard Generate()
// function does not need to exist.
func (t *DotnetSdkDev) Install() *dagger.Changeset {
	return t.WithInstall().Changes()
}

func (t *DotnetSdkDev) WithInstall() *DotnetSdkDev {
	t.Workspace = t.Workspace.
		WithoutDirectory(t.SourcePath).
		WithDirectory(t.SourcePath, dag.Directory().
			WithFile(
				"Dagger.SDK/introspection.json",
				dag.DaggerEngine().IntrospectionJSON(),
			))

	return t
}

// Run Cshapier (https://csharpier.com) on the SDK source code and
// save it back to the workspace.
func (t *DotnetSdkDev) WithFormat() *DotnetSdkDev {
	t.Workspace = dag.Directory().
		// We need to create a root directory to get the result or it will not
		//  be comparable with the original workspace
		WithDirectory("/",
			t.DevContainer().
				WithExec([]string{"dotnet", "tool", "restore"}).
				WithExec([]string{"dotnet", "csharpier", "."}).
				Rootfs().
				Directory("/src"),
		)

	return t
}

func (t *DotnetSdkDev) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}
