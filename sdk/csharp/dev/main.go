// A dev module for dagger-csharp-sdk.
//
// This module contains functions for developing the SDK such as, running tests,
// generate introspection, etc.
package main

import (
	"context"
	"fmt"

	"csharp-sdk-dev/internal/dagger"
)

func New(
	// C# SDK source.
	//
	// +optional
	// +defaultPath=".."
	// +ignore=["**/*","!src/**/*.cs","!src/**/*.csproj","!src/**/*.sln","!LICENSE","!README.md"]
	source *dagger.Directory,

	// Base container.
	//
	// +optional
	container *dagger.Container,
) *CsharpSdkDev {
	if container == nil {
		container = dag.Container().From("mcr.microsoft.com/dotnet/sdk:10.0")
	}
	path := "/src/sdk/csharp"
	return &CsharpSdkDev{
		Container: container.
			WithDirectory(path, source).
			WithWorkdir(path + "/src"),
	}
}

type CsharpSdkDev struct {
	Container *dagger.Container
}

// Generate code from introspection json file.
func (m *CsharpSdkDev) Generate(introspectionJSON *dagger.File) *dagger.Directory {
	return dag.Directory().WithFile("src/introspection.json", introspectionJSON)
}

// Testing the SDK.
func (m *CsharpSdkDev) Test(ctx context.Context, introspectionJSON *dagger.File) error {
	_, err := m.Container.
		WithFile("introspection.json", introspectionJSON).
		WithExec([]string{"dotnet", "restore"}).
		WithExec([]string{"dotnet", "build", "--no-restore"}).
		WithExec([]string{"dotnet", "test", "--no-build"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Sync(ctx)
	return err
}

// Lint all C# source files in the SDK.
func (m *CsharpSdkDev) Lint(ctx context.Context) error {
	// Install dotnet format tool and run it
	_, err := m.Container.
		WithExec([]string{"dotnet", "format", "--verify-no-changes"}).
		Sync(ctx)
	return err
}

// Format all C# source files.
func (m *CsharpSdkDev) Format() *dagger.Directory {
	return m.Container.
		WithExec([]string{"dotnet", "format"}).
		Directory("..")
}

// Pack the Dagger.SDK into a NuGet package.
func (m *CsharpSdkDev) Pack(
	ctx context.Context,
	introspectionJSON *dagger.File,
	// +optional
	// +default="Release"
	configuration string,
) *dagger.Directory {
	return m.Container.
		WithFile("introspection.json", introspectionJSON).
		WithExec([]string{
			"dotnet", "pack",
			"Dagger.SDK/Dagger.SDK.csproj",
			"-c", configuration,
			"-o", "/packages",
		}).
		Directory("/packages")
}

// Publish the Dagger.SDK to NuGet.
func (m *CsharpSdkDev) Publish(
	ctx context.Context,
	introspectionJSON *dagger.File,

	// +optional
	version string,

	// +optional
	nugetToken *dagger.Secret,

	// +optional
	dryRun bool,
) error {
	// Build the codegen CLI tool
	codegenBinary := m.Container.
		WithWorkdir("/src/sdk/csharp/codegen").
		WithExec([]string{"dotnet", "build", "Codegen.csproj", "-c", "Release"}).
		WithExec([]string{"dotnet", "publish", "Codegen.csproj", "-c", "Release", "-o", "/codegen-bin"}).
		Directory("/codegen-bin")

	// Generate Dagger.SDK.g.cs using the codegen tool
	generatedCode := m.Container.
		WithDirectory("/codegen", codegenBinary).
		WithFile("/schema.json", introspectionJSON).
		WithExec([]string{
			"dotnet", "/codegen/dagger-codegen.dll",
			"/schema.json", "/generated.cs",
		}).
		File("/generated.cs")

	// Add generated code to SDK and build package
	packaged := m.Container.
		WithFile("Dagger.SDK/Dagger.SDK.g.cs", generatedCode).
		WithExec([]string{
			"dotnet", "pack",
			"Dagger.SDK/Dagger.SDK.csproj",
			"-c", "Release",
			"-p:Version=" + version,
			"-o", "/packages",
		})

	if dryRun {
		// For dry-run, just verify the package was created
		entries, err := packaged.Directory("/packages").Entries(ctx)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("no packages were created")
		}
		return nil
	}

	// Publish to NuGet.org
	if nugetToken == nil {
		return fmt.Errorf("nuget-token is required for publishing")
	}

	_, err := packaged.
		WithSecretVariable("NUGET_API_KEY", nugetToken).
		WithExec([]string{
			"sh", "-c",
			"dotnet nuget push /packages/*.nupkg --api-key $NUGET_API_KEY --source https://api.nuget.org/v3/index.json",
		}).
		Sync(ctx)

	return err
}
