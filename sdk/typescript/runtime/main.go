package main

import (
	"context"
	"fmt"
	"typescript-sdk/internal/dagger"
)

func New(
	// +optional
	sdkSourceDir *dagger.Directory,
) *TypescriptSdk {
	return &TypescriptSdk{
		SDKSourceDir: sdkSourceDir,
	}
}

type TypescriptSdk struct {
	SDKSourceDir *dagger.Directory
}

const (
	ModSourceDirPath         = "/src"
	EntrypointExecutableFile = "__dagger.entrypoint.ts"

	SrcDir         = "src"
	GenDir         = "sdk"
	NodeModulesDir = "node_modules"

	schemaPath             = "/schema.json"
	dependenciesConfigPath = "/dependencies.json"
	codegenBinPath         = "/codegen"
)

// ModuleRuntime implements the `ModuleRuntime` method from the SDK module interface.
//
// It returns a ready to call container with the correct node, bun or deno runtime setup.
// On call, this will trigger the entrypoint that will either introspect and register the
// module in the Dagger engine or execute a function of that module.
//
// The returned container has the codegen freshly generated and any necessary dependency
// installed.
func (t *TypescriptSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	cfg, err := analyzeModuleConfig(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze module config: %w", err)
	}

	return runtimeBaseContainer(cfg, t.SDKSourceDir).
		withConfiguredRuntimeEnvironment().
		withGeneratedSDK(introspectionJSON).
		withSetupPackageManager().
		withInstalledDependencies().
		withUserSourceCode().
		withEntrypoint().
		Container(), nil
}

// Codegen implements the `Codegen` method from the SDK module interface.
//
// It returns the generated API client based on user's module as well as
// ignore directive regarding the generated content.
func (t *TypescriptSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	cfg, err := analyzeModuleConfig(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze module config: %w", err)
	}

	runtimeBaseCtr := runtimeBaseContainer(cfg, t.SDKSourceDir).
		withConfiguredRuntimeEnvironment().
		withGeneratedSDK(introspectionJSON).
		withSetupPackageManager().
		withGeneratedLockFile().
		withUserSourceCode()

	// Check if there's any user source files, if not, add the template file.
	// NOTE: This should be moved in a `Init` function once we improve the SDK interface.
	sourcesFiles, err := runtimeBaseCtr.Container().Directory(".").Glob(ctx, "src/**/*.ts")
	if err != nil {
		return nil, fmt.Errorf("failed to list user source files: %w", err)
	}

	if len(sourcesFiles) == 0 {
		runtimeBaseCtr = runtimeBaseCtr.withInitTemplate()
	}

	// Extract codegen directory
	codegen := dag.
		Directory().
		WithDirectory(
			"/",
			runtimeBaseCtr.ModuleDirectory(),
			dagger.DirectoryWithDirectoryOpts{Exclude: []string{"**/node_modules", "**/.pnpm-store"}},
		)

	return dag.GeneratedCode(
		codegen,
	).
		WithVCSGeneratedPaths([]string{
			GenDir + "/**",
		}).
		WithVCSIgnoredPaths([]string{
			GenDir,
			"**/node_modules/**",
			"**/.pnpm-store/**",
		}), nil
}

// Returns the list of files that are copied from the host when generating the client.
func (t *TypescriptSdk) RequiredClientGenerationFiles() []string {
	return []string{
		"./package.json",
		"./tsconfig.json",
		"./deno.json",
	}
}

// Returns a directory with a standalone generated client and any necessary configuration
// files that are required to work.
func (t *TypescriptSdk) GenerateClient(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
	outputDir string,
) (*dagger.Directory, error) {
	cfg, err := analyzeModuleConfig(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze module config: %w", err)
	}

	moduleSourceID, err := modSource.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module source id: %w", err)
	}

	return clientGenBaseContainer(cfg, t.SDKSourceDir).
		withBundledSDK().
		withGeneratedClient(introspectionJSON, moduleSourceID, outputDir).
		withUpdatedEnvironment(outputDir).
		GeneratedDirectory(), nil
}
