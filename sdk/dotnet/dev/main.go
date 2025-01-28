// A dev module for dagger-dotnet-sdk.
//
// This module contains functions for developing the SDK such as, running tests,
// generate introspection, etc.
package main

import (
	"context"
	_ "embed"

	"github.com/dagger/dagger/sdk/dotnet/dev/internal/dagger"
	"golang.org/x/sync/errgroup"
)

func New(
	// Dotnet SDK source.
	//
	// +optional
	// +defaultPath=".."
	// +ignore=["**/*","!sdk/.config","!sdk/Dagger.sln","!sdk/Dagger.sln.DotSettings.user","!sdk/global.json","!sdk/**/*.cs","!sdk/**/*.csproj"]
	source *dagger.Directory,

	// Base container.
	//
	// +optional
	container *dagger.Container,
) *DotnetSdkDev {
	if container == nil {
		container = dag.Container().From("mcr.microsoft.com/dotnet/sdk:8.0-alpine3.20")
	}
	path := "/src/sdk/dotnet"
	return &DotnetSdkDev{
		Container: container.
			WithDirectory(path, source).
			WithWorkdir(path + "/sdk").
			WithExec([]string{"dotnet", "tool", "restore"}),
	}
}

type DotnetSdkDev struct {
	Container *dagger.Container
}

// Generate code from introspection json file.
func (m *DotnetSdkDev) Generate(introspectionJSON *dagger.File) *dagger.Directory {
	return dag.Directory().WithFile("sdk/Dagger.SDK/introspection.json", introspectionJSON)
}

// Testing the SDK.
func (m *DotnetSdkDev) Test(ctx context.Context, introspectionJSON *dagger.File) error {
	_, err := m.Container.
		WithFile("Dagger.SDK/introspection.json", introspectionJSON).
		WithExec([]string{"dotnet", "restore"}).
		WithExec([]string{"dotnet", "build", "--no-restore"}).
		WithExec([]string{"dotnet", "test", "--no-build"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Sync(ctx)
	return err
}

// Lint all CSharp source files in the SDK.
func (m *DotnetSdkDev) Lint(ctx context.Context) error {
	_, err := m.Container.
		WithExec([]string{"dotnet", "csharpier", "--check", "."}).
		Sync(ctx)
	return err
}

// Run test and lint.
func (m *DotnetSdkDev) Check(ctx context.Context, introspectionJSON *dagger.File) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "test")
		defer span.End()
		return m.Test(ctx, introspectionJSON)
	})
	eg.Go(func() error {
		ctx, span := Tracer().Start(ctx, "lint")
		defer span.End()
		return m.Lint(ctx)
	})
	return eg.Wait()
}

// Format all CSharp source files.
func (m *DotnetSdkDev) Format() *dagger.Directory {
	return m.Container.
		WithExec([]string{"dotnet", "csharpier", "."}).
		Directory("..")
}
