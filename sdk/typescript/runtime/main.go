package main

import (
	"context"
	"fmt"

	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsutils"

	"github.com/iancoleman/strcase"
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
// On call, this will trigger the entrypoint that will either intropect and register the
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

	switch cfg.runtime {
	case Bun:
		return NewBunRuntime(cfg, t.SDKSourceDir, introspectionJSON).SetupContainer(ctx)
	case Deno:
		return NewDenoRuntime(cfg, t.SDKSourceDir, introspectionJSON).SetupContainer(ctx)
	case Node:
		return NewNodeRuntime(cfg, t.SDKSourceDir, introspectionJSON).SetupContainer(ctx)
	default:
		return nil, fmt.Errorf("unknown runtime %s", cfg.runtime)
	}
}

func (t *TypescriptSdk) ModuleTypes(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
	outputFilePath string,
) (*dagger.Container, error) {
	cfg, err := analyzeModuleConfig(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze module config: %w", err)
	}

	// TODO(TomChv): Update the TypeScript Codegen so it doesn't rely on moduleSourcePath anymore.
	clientBindings := NewLibGenerator(t.SDKSourceDir).
		GenerateBindings(introspectionJSON, cfg.name, cfg.modulePath(), Bundle)

	return NewIntrospector(t.SDKSourceDir).
		AsEntrypoint(outputFilePath,
			cfg.name,
			modSource.ContextDirectory().Directory(cfg.subPath).Directory("src"),
			clientBindings,
		), nil
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

	var codegen *dagger.Directory
	switch cfg.runtime {
	case Bun:
		codegen, err = NewBunRuntime(cfg, t.SDKSourceDir, introspectionJSON).GenerateDir(ctx)
	case Deno:
		codegen, err = NewDenoRuntime(cfg, t.SDKSourceDir, introspectionJSON).GenerateDir(ctx)
	case Node:
		codegen, err = NewNodeRuntime(cfg, t.SDKSourceDir, introspectionJSON).GenerateDir(ctx)
	default:
		return nil, fmt.Errorf("unknown runtime %s", cfg.runtime)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate runtime dir: %w", err)
	}

	// TODO: handle that in an init method.
	// Add default template if no source files exist
	sourceFiles, err := cfg.source.Glob(ctx, "src/**/*.ts")
	if err != nil {
		return nil, fmt.Errorf("failed to list source files: %w", err)
	}

	if len(sourceFiles) == 0 {
		codegen = codegen.WithFile(
			"src/index.ts",
			dag.File("index.ts", tsutils.TemplateIndexTS(strcase.ToCamel(cfg.name))))
	}

	return dag.GeneratedCode(
		dag.Directory().WithDirectory(cfg.subPath, codegen),
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
