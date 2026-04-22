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
	clientBindings := NewLibGenerator(t.SDKSourceDir, cfg.libGeneratorOpts()).
		GenerateBindings(introspectionJSON, Bundle, ModSourceDirPath)

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
	srcDirExist, err := cfg.source.Exists(ctx, SrcDir)
	if err != nil {
		return nil, fmt.Errorf("failed to check if src dir exists: %w", err)
	}

	sourceFiles := []string{}
	if srcDirExist {
		sourceFiles, err = cfg.source.Glob(ctx, "src/**/*.ts")
		if err != nil {
			return nil, fmt.Errorf("failed to list source files: %w", err)
		}
	}

	if len(sourceFiles) == 0 {
		codegen = codegen.WithNewFile(
			"src/index.ts",
			tsutils.TemplateIndexTS(strcase.ToCamel(cfg.name)))
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
		"**/package.json",
		"**/tsconfig.json",
		"**/deno.json",
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
	cfg, err := analyzeClientConfig(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze module config: %w", err)
	}

	moduleSourceID, err := modSource.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module source id: %w", err)
	}

	result := dag.Directory()

	libGenerator := NewLibGenerator(t.SDKSourceDir, &LibGeneratorOpts{
		moduleSourceID:    string(moduleSourceID),
		genClient:         true,
		coexistWithModule: cfg.sdk != "" && cfg.subPath == ".",
	})

	if cfg.sdkLibOrigin == Remote {
		result = result.WithDirectory(
			outputDir,
			libGenerator.GenerateRemoteLibrary(introspectionJSON, outputDir),
			dagger.DirectoryWithDirectoryOpts{
				Include: []string{"client.gen.ts"},
			})
	} else {
		genDir := libGenerator.GenerateBundleLibrary(introspectionJSON, outputDir)

		result = result.
			WithDirectory("sdk", genDir, dagger.DirectoryWithDirectoryOpts{
				Exclude: []string{"client.gen.ts"},
			}).
			WithDirectory(outputDir, genDir, dagger.DirectoryWithDirectoryOpts{
				Include: []string{"client.gen.ts"},
			})
	}

	if cfg.runtime != Deno {
		tsconfig, err := CreateOrUpdateTSConfigForClient(ctx, cfg.source, cfg.sdkLibOrigin == Remote)
		if err != nil {
			return nil, fmt.Errorf("failed to create or update tsconfig.json")
		}

		packageJSONFile := defaultPackageJSONFile()
		if cfg.packageJSONConfig != nil {
			packageJSONFile = cfg.source.File("package.json")
		}

		packageJSONFile, err = CreateOrUpdatePackageJSONForClient(ctx, packageJSONFile)
		if err != nil {
			return nil, fmt.Errorf("failed to update package.json: %w", err)
		}

		result = result.
			WithFile("tsconfig.json", tsconfig).
			WithFile("package.json", packageJSONFile)
	} else {
		denojson, err := UpdateDenoJSONForClient(ctx, cfg.source.File("deno.json"), cfg.sdkLibOrigin == Remote)
		if err != nil {
			return nil, fmt.Errorf("failed to update deno.json")
		}

		result = result.WithFile("deno.json", denojson)
	}

	return dag.Directory().WithDirectory(cfg.modPath, result), nil
}
