package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/iancoleman/strcase"
	"github.com/tidwall/gjson"
)

const (
	bunVersion  = "1.0.27"
	nodeVersion = "21.3"

	nodeImageDigest = "sha256:3dab5cc219983a5f1904d285081cceffc9d181e64bed2a4a18855d2d62c64ccb"
	bunImageDigest  = "sha256:82d3d3b8ad96c4eea45c88167ce46e7e24afc726897d48e48cc6d6bf230c061c"

	nodeImageRef = "node:" + nodeVersion + "-alpine@" + nodeImageDigest
	bunImageRef  = "oven/bun:" + bunVersion + "-alpine@" + bunImageDigest
)

type SupportedTSRuntime string

const (
	Bun  SupportedTSRuntime = "bun"
	Node SupportedTSRuntime = "node"
)

func New(
	// +optional
	sdkSourceDir *Directory,
) *TypeScriptSdk {
	return &TypeScriptSdk{
		SDKSourceDir: sdkSourceDir,
		RequiredPaths: []string{
			"**/package.json",
			"**/package-lock.json",
			"**/tsconfig.json",
		},
	}
}

type TypeScriptSdk struct {
	SDKSourceDir  *Directory
	RequiredPaths []string
}

const (
	ModSourceDirPath         = "/src"
	EntrypointExecutableFile = "__dagger.entrypoint.ts"

	SrcDir = "src"
	GenDir = "sdk"

	schemaPath     = "/schema.json"
	codegenBinPath = "/codegen"
)

// ModuleRuntime returns a container with the node or bun entrypoint ready to be called.
func (t *TypeScriptSdk) ModuleRuntime(ctx context.Context, modSource *ModuleSource, introspectionJson string) (*Container, error) {
	ctr, err := t.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	detectedRuntime, err := t.DetectRuntime(ctx, modSource, subPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create module runtime: %w", err)
	}

	entrypointPath := filepath.Join(ModSourceDirPath, subPath, SrcDir, EntrypointExecutableFile)
	tsConfigPath := filepath.Join(ModSourceDirPath, subPath, "tsconfig.json")

	ctr = ctr.WithMountedFile(entrypointPath, ctr.Directory("/opt/module/bin").File(EntrypointExecutableFile))

	switch detectedRuntime {
	case Bun:
		return ctr.
			// Install dependencies
			WithExec([]string{"bun", "install"}).
			WithEntrypoint([]string{"bun", entrypointPath}), nil
	case Node:
		return ctr.
			// Install dependencies
			WithExec([]string{"npm", "install"}).
			// need to specify --tsconfig because final runtime container will change working directory to a separate scratch
			// dir, without this the paths mapped in the tsconfig.json will not be used and js module loading will fail
			// need to specify --no-deprecation because the default package.json has no main field which triggers a warning
			// not useful to display to the user.
			WithEntrypoint([]string{"tsx", "--no-deprecation", "--tsconfig", tsConfigPath, entrypointPath}), nil
	default:
		return nil, fmt.Errorf("unknown runtime: %v", detectedRuntime)
	}
}

// Codegen returns the generated API client based on user's module
func (t *TypeScriptSdk) Codegen(ctx context.Context, modSource *ModuleSource, introspectionJson string) (*GeneratedCode, error) {
	// Get base container
	ctr, err := t.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}
	dir := dag.Directory().WithDirectory("", ctr.Directory(ModSourceDirPath))

	return dag.GeneratedCode(dir).
		WithVCSGeneratedPaths([]string{
			GenDir + "/**",
		}).
		WithVCSIgnoredPaths([]string{
			GenDir,
			"node_modules/**",
		}), nil
}

// CodegenBase returns a Container containing the SDK from the engine container
// and the user's code with a generated API based on what he did.
func (t *TypeScriptSdk) CodegenBase(ctx context.Context, modSource *ModuleSource, introspectionJson string) (*Container, error) {
	// Load module name for the template class
	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	detectedRuntime, err := t.DetectRuntime(ctx, modSource, subPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create codegen base: %w", err)
	}

	base, err := t.Base(detectedRuntime)
	if err != nil {
		return nil, err
	}

	gen := base.
		// Add codegen binary into a special path
		WithMountedFile(codegenBinPath, t.SDKSourceDir.File("/codegen")).
		// Add introspection file
		WithNewFile(schemaPath, ContainerWithNewFileOpts{
			Contents: introspectionJson,
		}).
		// Execute the code generator using the given introspection file
		WithExec([]string{
			codegenBinPath,
			"--lang", "typescript",
			"--output", ModSourceDirPath,
			"--module-name", name,
			"--module-path", filepath.Join(ModSourceDirPath, subPath),
			"--introspection-json-path", schemaPath,
		}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory(filepath.Join(ModSourceDirPath, subPath, GenDir))

	base = base.
		// Add codegen binary into a special path
		WithMountedFile(codegenBinPath, t.SDKSourceDir.File("/codegen")).
		// Add template directory
		WithMountedDirectory("/opt/module", dag.CurrentModule().Source().Directory(".")).
		// Mount users' module
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithWorkdir(filepath.Join(ModSourceDirPath, subPath)).
		// Add sdk source code
		WithDirectory(GenDir, t.SDKSourceDir, ContainerWithDirectoryOpts{
			Exclude: []string{"codegen", "runtime"},
		}).
		// Add generated code
		WithDirectory(GenDir, gen)

	switch detectedRuntime {
	case Bun:
		base = base.
			// Check if the project has existing source:
			// if it does: add sdk as dev dependency
			// if not: copy the template and replace QuickStart with the module name
			WithExec([]string{"sh", "-c",
				"if [ -f package.json ]; then  bun install ./sdk  --dev  && bun /opt/module/bin/__tsconfig.updator.ts; else cp -r /opt/module/template/*.json .; fi",
			})
	case Node:
		base = base.
			// Check if the project has existing source:
			// if it does: add sdk as dev dependency
			// if not: copy the template and replace QuickStart with the module name
			WithExec([]string{"sh", "-c",
				"if [ -f package.json ]; then  npm install --package-lock-only ./sdk  --dev  && tsx /opt/module/bin/__tsconfig.updator.ts; else cp -r /opt/module/template/*.json .; fi",
			})
	default:
		return nil, fmt.Errorf("unknown runtime: %v", detectedRuntime)
	}

	return base.
		// Check if there's a src directory with .ts files in it.
		// If not, add the template file and replace QuickStart with the module name
		// This cover the case where there's a package.json but no src directory.
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("mkdir -p src && if ls src/*.ts >/dev/null 2>&1; then true; else cp /opt/module/template/src/index.ts src/index.ts && sed -i -e 's/QuickStart/%s/g' ./src/index.ts; fi", strcase.ToCamel(name))},
		), nil
}

// Base returns a Node or Bun container with cache setup for yarn or bun
func (t *TypeScriptSdk) Base(runtime SupportedTSRuntime) (*Container, error) {
	switch runtime {
	case Bun:
		return dag.Container().
			From(bunImageRef).
			WithoutEntrypoint().
			WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(fmt.Sprintf("mod-bun-cache-%s", bunVersion))), nil
	case Node:
		return dag.Container().
			From(nodeImageRef).
			WithoutEntrypoint().
			WithMountedCache("/root/.npm", dag.CacheVolume(fmt.Sprintf("mod-npm-cache-%s", nodeVersion))).
			WithExec([]string{"npm", "install", "-g", "tsx"}), nil
	default:
		return nil, fmt.Errorf("unknown runtime: %v", runtime)
	}
}

// DetectRuntime returns the runtime(bun or node) detected for the user's module
// If a runtime is specfied inside the package.json, it will be used.
// If a package-lock.json, yarn.lock, or pnpm-lock.yaml is present, node will be used.
// If a bun.lockb is present, bun will be used.
// If none of the above is present, node will be used.
func (t *TypeScriptSdk) DetectRuntime(ctx context.Context, modSource *ModuleSource, subPath string) (SupportedTSRuntime, error) {
	// Try to detect runtime from package.json
	source := modSource.ContextDirectory().Directory(subPath)

	// read contents of package.json
	json, err := source.File("package.json").Contents(ctx)
	if err == nil {
		value := gjson.Get(json, "dagger.runtime").String()
		if value != "" {
			switch runtime := SupportedTSRuntime(value); runtime {
			case Bun, Node:
				return runtime, nil
			default:
				return "", fmt.Errorf("detected unknown runtime: %v", runtime)
			}
		}
	}

	// Try to detect runtime from lock files
	entries, err := source.Entries(ctx)
	if err == nil {
		if slices.Contains(entries, "package-lock.json") ||
			slices.Contains(entries, "yarn.lock") ||
			slices.Contains(entries, "pnpm-lock.yaml") {
			return Node, nil
		}
		if slices.Contains(entries, "bun.lockb") {
			return Bun, nil
		}
	}

	// Default to node
	return Node, nil
}
