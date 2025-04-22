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
// On call, this will trigger the entrypoint that will either intropect and register the
// module in the Dagger engine or execute a function of that module.
//
// The returned container has the codegen freshly generated and any necesary dependency
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

func (t *TypescriptSdk) RequiredClientGenerationFiles() []string {
	return []string{
		"./package.json",
		"./tsconfig.json",
		"./deno.json",
	}
}

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

	gitDepsJSON, err := extraGitDependenciesFromModule(ctx, modSource)
	if err != nil {
		return nil, fmt.Errorf("failed to get module dependencies: %w", err)
	}

	return clientGenBaseContainer(cfg, t.SDKSourceDir).
		withBundledGitDependenciesJSON(gitDepsJSON).
		withBundledSDK().
		withGeneratedClient(introspectionJSON, outputDir).
		withUpdatedEnvironment(outputDir).
		GeneratedDirectory(), nil
}

// func (t *TypescriptSdk) GenerateClient(
// 	ctx context.Context,
// 	modSource *dagger.ModuleSource,
// 	introspectionJSON *dagger.File,
// 	outputDir string,
// 	dev bool,
// ) (*dagger.Directory, error) {
// 	workdirPath := "/module"

// 	currentModuleDirectoryPath, err := modSource.SourceRootSubpath(ctx)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get module source root subpath: %w", err)
// 	}

// 	ctr := dag.Container().
// 		From(tsdistconsts.DefaultNodeImageRef).
// 		WithoutEntrypoint().
// 		// Add client config update file
// 		WithMountedFile(
// 			"/opt/__tsclientconfig.updator.ts",
// 			dag.CurrentModule().Source().Directory("bin").File("__tsclientconfig.updator.ts"),
// 		).
// 		// install tsx from its bundled location in the engine image
// 		WithMountedDirectory("/usr/local/lib/node_modules/tsx", t.SDKSourceDir.Directory("/tsx_module")).
// 		WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"}).
// 		// Add dagger codegen binary.
// 		WithMountedFile(codegenBinPath, t.SDKSourceDir.File("/codegen")).
// 		// Mount the introspection file.
// 		WithMountedFile(schemaPath, introspectionJSON).
// 		// Mount the current module directory.
// 		WithDirectory(workdirPath, modSource.ContextDirectory()).
// 		WithWorkdir(filepath.Join(workdirPath, currentModuleDirectoryPath))

// 	codegenArgs := []string{
// 		"/codegen",
// 		"--lang", "typescript",
// 		"--output", outputDir,
// 		"--introspection-json-path", schemaPath,
// 		fmt.Sprintf("--dev=%t", dev),
// 		"--client-only",
// 	}

// 	// Same data structure as ModuleConfigDependency from core/modules/config.go#L183
// 	type gitDependencyConfig struct {
// 		Name   string
// 		Pin    string
// 		Source string
// 	}

// 	dependencies, err := modSource.Dependencies(ctx)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get module dependencies: %w", err)
// 	}

// 	dependenciesConfig := []gitDependencyConfig{}
// 	// Add remote dependency reference to the codegen arguments.
// 	for _, dep := range dependencies {
// 		depKind, err := dep.Kind(ctx)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to get dependency kind: %w", err)
// 		}

// 		if depKind != dagger.ModuleSourceKindGitSource {
// 			continue
// 		}

// 		depSource, err := dep.AsString(ctx)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to get module dependency ref: %w", err)
// 		}

// 		depPin, err := dep.Pin(ctx)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to get module dependency pin: %w", err)
// 		}

// 		depName, err := dep.ModuleOriginalName(ctx)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to get module dependency name: %w", err)
// 		}

// 		dependenciesConfig = append(dependenciesConfig, gitDependencyConfig{
// 			Name:   depName,
// 			Pin:    depPin,
// 			Source: depSource,
// 		})
// 	}

// 	if len(dependenciesConfig) > 0 {
// 		depenciesJSONConfig, err := json.Marshal(dependenciesConfig)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to marshal dependencies config: %w", err)
// 		}

// 		ctr = ctr.WithNewFile(dependenciesConfigPath, string(depenciesJSONConfig))
// 		codegenArgs = append(codegenArgs,
// 			fmt.Sprintf("--dependencies-json-file-path=%s", dependenciesConfigPath),
// 		)
// 	}

// 	ctr = ctr.
// 		// Execute the code generator using the given introspection file.
// 		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
// 			ExperimentalPrivilegedNesting: true,
// 		})

// 	if dev {
// 		ctr = ctr.WithDirectory("./sdk", t.SDKSourceDir.
// 			Directory("/bundled_lib").
// 			WithDirectory("/", dag.CurrentModule().Source().Directory("bundled_static_export/client")),
// 		).
// 			WithExec([]string{"tsx", "/opt/__tsclientconfig.updator.ts", "--dev=true", fmt.Sprintf("--library-dir=%s", outputDir)})
// 	} else {
// 		ctr = ctr.
// 			WithExec([]string{"npm", "pkg", "set", "dependencies[@dagger.io/dagger]=@dagger.io/dagger"}).
// 			WithExec([]string{"tsx", "/opt/__tsclientconfig.updator.ts", "--dev=false", fmt.Sprintf("--library-dir=%s", outputDir)})
// 	}

// 	return dag.Directory().WithDirectory("/", ctr.Directory(workdirPath)), nil
// }
