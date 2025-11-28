// Runtime module for the C# SDK

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"csharp-sdk/internal/dagger"
)

const (
	DotnetImage   = "mcr.microsoft.com/dotnet/sdk:10.0"
	ModSourcePath = "/src"
	GenPath       = "sdk"
)

type CsharpSdk struct {
	SourceDir *dagger.Directory
}

func New(
	// Directory with the C# SDK source code.
	// +optional
	// +defaultPath=".."
	// +ignore=["**/bin","**/obj","**/.vs","**/.vscode"]
	sdkSourceDir *dagger.Directory,
) (*CsharpSdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &CsharpSdk{
		SourceDir: sdkSourceDir,
	}, nil
}

func (m *CsharpSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	return dag.
		GeneratedCode(ctr.Directory(ModSourcePath)).
		WithVCSGeneratedPaths([]string{
			GenPath + "/**",
		}).
		WithVCSIgnoredPaths([]string{GenPath, "bin", "obj"}), nil
}

// CodegenBase prepares the base container with SDK and template files (no build)
// +cache="never"
func (m *CsharpSdk) CodegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module name: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module source path: %w", err)
	}

	base := dag.Container().
		From(DotnetImage).
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "-y", "git"})

	srcPath := filepath.Join(ModSourcePath, subPath)
	sdkPath := filepath.Join(srcPath, GenPath)
	runtime := dag.CurrentModule().Source()

	ctxDir := modSource.ContextDirectory().
		WithoutDirectory(filepath.Join(subPath, "bin")).
		WithoutDirectory(filepath.Join(subPath, "obj")).
		WithoutDirectory(filepath.Join(subPath, GenPath))

	// Build the standalone codegen CLI tool
	codegenBinary := base.
		WithDirectory("/codegen-src", m.SourceDir).
		WithWorkdir("/codegen-src/csharp/codegen").
		WithExec([]string{"dotnet", "build", "Codegen.csproj", "-c", "Release"}).
		WithExec([]string{"dotnet", "publish", "Codegen.csproj", "-c", "Release", "-o", "/codegen-bin"}).
		Directory("/codegen-bin")

	// Generate Dagger.SDK.g.cs using the codegen tool
	generatedCode := base.
		WithDirectory("/codegen", codegenBinary).
		WithFile("/schema.json", introspectionJSON).
		WithExec([]string{
			"dotnet", "/codegen/dagger-codegen.dll",
			"/schema.json", "/generated.cs",
		}).
		File("/generated.cs")

	// Build the analyzers DLL
	analyzerBuild := base.
		WithDirectory("/analyzer-src", m.SourceDir).
		WithWorkdir("/analyzer-src/csharp/src/Dagger.SDK.Analyzers").
		WithExec([]string{"dotnet", "restore", "Dagger.SDK.Analyzers.csproj", "--verbosity", "minimal"}).
		WithExec([]string{"dotnet", "build", "Dagger.SDK.Analyzers.csproj", "-c", "Release", "--no-restore"})

	analyzerDll := analyzerBuild.File("/analyzer-src/csharp/src/Dagger.SDK.Analyzers/bin/Release/netstandard2.0/Dagger.SDK.Analyzers.dll")

	// Build the code fixes DLL separately
	codeFixesBuild := base.
		WithDirectory("/codefixes-src", m.SourceDir).
		WithWorkdir("/codefixes-src/csharp/src/Dagger.SDK.CodeFixes").
		WithExec([]string{"dotnet", "restore", "Dagger.SDK.CodeFixes.csproj", "--verbosity", "minimal"}).
		WithExec([]string{"dotnet", "build", "Dagger.SDK.CodeFixes.csproj", "-c", "Release", "--no-restore"})

	codeFixesDll := codeFixesBuild.File("/codefixes-src/csharp/src/Dagger.SDK.CodeFixes/bin/Release/netstandard2.0/Dagger.SDK.CodeFixes.dll")

	// The SDK csproj is different for vendored sdk as it does not reference other projects
	// We create a clean version here
	cleanCsproj := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0</TargetFramework>
    <ImplicitUsings>enable</ImplicitUsings>
    <Nullable>enable</Nullable>
    <LangVersion>latest</LangVersion>
    <RootNamespace>Dagger</RootNamespace>
  </PropertyGroup>

  <ItemGroup>
    <PackageReference Include="System.Collections.Immutable" Version="10.0.0" />
  </ItemGroup>
</Project>`

	// Prepare SDK source with generated code and clean project file
	sdkSource := base.
		WithDirectory("/sdk-src", m.SourceDir).
		WithWorkdir("/sdk-src/csharp/src/Dagger.SDK").
		// Remove bin/obj build artifacts if they exist
		WithExec([]string{"sh", "-c", "rm -rf bin obj || true"}).
		// Replace the .csproj with a clean one (no broken ProjectReferences)
		WithNewFile("Dagger.SDK.csproj", cleanCsproj).
		// Add the generated Dagger API code
		WithFile("Dagger.SDK.g.cs", generatedCode).
		// Add analyzers directory with the built DLLs (both analyzers and code fixes)
		WithDirectory("analyzers/dotnet/cs", dag.Directory().
			WithFile("Dagger.SDK.Analyzers.dll", analyzerDll).
			WithFile("Dagger.SDK.CodeFixes.dll", codeFixesDll)).
		// Return the SDK source directory (includes Module/ for runtime functionality)
		Directory("/sdk-src/csharp/src/Dagger.SDK")

	// Mount the module source directory
	ctr := base.
		WithMountedDirectory(ModSourcePath, ctxDir).
		WithWorkdir(srcPath).
		// Copy SDK with generated code
		WithDirectory(sdkPath, sdkSource)

	// Initialize module if needed (copy template files)
	entries, err := ctr.Directory(srcPath).Entries(ctx)
	if err != nil {
		return nil, err
	}

	// Check if this is a new module (no Main.cs or Program.cs)
	hasMainCs := false
	hasProgramCs := false
	hasProjectFile := false
	for _, entry := range entries {
		if entry == "Main.cs" {
			hasMainCs = true
		}
		if entry == "Program.cs" {
			hasProgramCs = true
		}
		projectFileName := toPascalCase(name) + ".csproj"
		if entry == projectFileName {
			hasProjectFile = true
		}
	}

	// If this is a new module, copy template files
	if !hasMainCs && !hasProgramCs && !hasProjectFile {
		// Convert module name to valid C# identifier (PascalCase)
		className := toPascalCase(name)
		projectFileName := className + ".csproj"
		
		ctr = ctr.
			WithDirectory(".", runtime.Directory("template")).
			// Rename DaggerModule.csproj to {ModuleName}.csproj
			WithExec([]string{"mv", "DaggerModule.csproj", projectFileName}).
			// Replace all occurrences of DaggerModule with the actual module name
			WithExec([]string{"sh", "-c", fmt.Sprintf(
				"sed -i 's/DaggerModule/%s/g' Main.cs %s",
				className, projectFileName,
			)})
	}

	return ctr, nil
}

// toPascalCase converts a module name to a valid C# PascalCase identifier
// Examples: "my-module" -> "MyModule", "my_module" -> "MyModule", "123test" -> "Test123"
func toPascalCase(name string) string {
	if name == "" {
		return "DaggerModule"
	}

	// Split on hyphens, underscores, and spaces
	var parts []string
	currentPart := ""
	for _, r := range name {
		if r == '-' || r == '_' || r == ' ' {
			if currentPart != "" {
				parts = append(parts, currentPart)
				currentPart = ""
			}
		} else {
			currentPart += string(r)
		}
	}
	if currentPart != "" {
		parts = append(parts, currentPart)
	}

	if len(parts) == 0 {
		return "DaggerModule"
	}

	// Convert each part to title case
	result := ""
	for _, part := range parts {
		if len(part) > 0 {
			// Capitalize first letter, lowercase rest
			firstRune := []rune(part)[0]
			if firstRune >= 'a' && firstRune <= 'z' {
				firstRune = firstRune - 32 // Convert to uppercase
			}
			rest := ""
			if len(part) > 1 {
				rest = part[1:]
			}
			result += string(firstRune) + rest
		}
	}

	// Ensure first character is a letter (remove leading digits)
	for len(result) > 0 {
		firstRune := []rune(result)[0]
		if (firstRune >= 'A' && firstRune <= 'Z') || (firstRune >= 'a' && firstRune <= 'z') {
			break
		}
		if len(result) > 1 {
			result = result[1:]
		} else {
			return "DaggerModule"
		}
	}

	if result == "" {
		return "DaggerModule"
	}

	return result
}

func (m *CsharpSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module name: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module source path: %w", err)
	}

	srcPath := filepath.Join(ModSourcePath, subPath)

	// Find the project file (DaggerModule.csproj or {Name}.csproj)
	projectFile, err := findProjectFile(ctx, ctr, srcPath, name)
	if err != nil {
		return nil, err
	}

	// Build the module with specific project file
	ctr = ctr.WithExec([]string{"dotnet", "build", projectFile, "-c", "Release"})

	// Set the entrypoint to run the compiled program
	ctr = ctr.WithEntrypoint([]string{
		"dotnet", "run", "--project", projectFile, "--no-build", "-c", "Release", "--",
	})

	return ctr, nil
}

// findProjectFile finds the .csproj file in the source directory
// It looks for {moduleName}.csproj
func findProjectFile(ctx context.Context, ctr *dagger.Container, srcPath string, moduleName string) (string, error) {
	entries, err := ctr.Directory(srcPath).Entries(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list directory: %w", err)
	}

	// Check for {moduleName}.csproj first (primary convention)
	moduleProjectName := toPascalCase(moduleName) + ".csproj"
	for _, entry := range entries {
		if entry == moduleProjectName {
			return filepath.Join(srcPath, moduleProjectName), nil
		}
	}

	return "", fmt.Errorf("no .csproj file found (looking for %s)", moduleProjectName)
}
