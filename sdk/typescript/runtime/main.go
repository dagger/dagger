package main

import (
	"context"
	"fmt"
	"path"
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
) *TypescriptSdk {
	return &TypescriptSdk{
		SDKSourceDir: sdkSourceDir,
		RequiredPaths: []string{
			"**/package.json",
			"**/package-lock.json",
			"**/tsconfig.json",
		},
	}
}

type TypescriptSdk struct {
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
func (t *TypescriptSdk) ModuleRuntime(ctx context.Context, modSource *ModuleSource, introspectionJSON string) (*Container, error) {
	ctr, err := t.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
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
			WithExec([]string{"yarn", "install"}).
			// need to specify --tsconfig because final runtime container will change working directory to a separate scratch
			// dir, without this the paths mapped in the tsconfig.json will not be used and js module loading will fail
			// need to specify --no-deprecation because the default package.json has no main field which triggers a warning
			// not useful to display to the user.
			WithEntrypoint([]string{"tsx", "--no-deprecation", "--tsconfig", tsConfigPath, entrypointPath}), nil
	default:
		return nil, fmt.Errorf("unknown runtime: %s", detectedRuntime)
	}
}

// Codegen returns the generated API client based on user's module
func (t *TypescriptSdk) Codegen(ctx context.Context, modSource *ModuleSource, introspectionJSON string) (*GeneratedCode, error) {
	// Get base container
	ctr, err := t.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
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
func (t *TypescriptSdk) CodegenBase(ctx context.Context, modSource *ModuleSource, introspectionJSON string) (*Container, error) {
	// Load module name for the template class
	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
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
			Contents: introspectionJSON,
		}).
		// Execute the code generator using the given introspection file
		WithExec([]string{
			codegenBinPath,
			"--lang", "typescript",
			"--output", ModSourceDirPath,
			"--module-name", name,
			"--module-context-path", filepath.Join(ModSourceDirPath, subPath),
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
		// Add generated code
		WithDirectory(GenDir, gen)

	moduleFiles, err := base.Directory(".").Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list rootfs entries: %v", err)
	}
	packageJsonExist := slices.Contains(moduleFiles, "package.json")

	switch detectedRuntime {
	case Bun:
		// Check if the project has existing source:
		// if it does: update the tsconfig in case dagger isn't part of the configuration
		// if not: copy the template and replace QuickStart with the module name
		if packageJsonExist {
			base = base.WithExec([]string{"bun", "/opt/module/bin/__tsconfig.updator.ts"})
		} else {
			base = base.WithDirectory(".", base.Directory("/opt/module/template"), ContainerWithDirectoryOpts{Include: []string{"*.json"}})
		}

	case Node:
		// Check if the project has existing source:
		// if it does: update the tsconfig in case dagger isn't part of the configuration
		// if not: copy the template and replace QuickStart with the module name
		if packageJsonExist {
			base = base.WithExec([]string{"tsx", "/opt/module/bin/__tsconfig.updator.ts"})
		} else {
			base = base.WithDirectory(".", base.Directory("/opt/module/template"), ContainerWithDirectoryOpts{Include: []string{"*.json"}})
		}
	default:
		return nil, fmt.Errorf("unknown runtime: %s", detectedRuntime)
	}

	if !slices.Contains(moduleFiles, "src") {
		base = base.WithDirectory("src", dag.Directory())
	}

	moduleSourceFiles, err := base.Directory("src").Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list module source entries: %v", err)
	}

	// Check if there's a src directory with .ts files in it.
	// If not, add the template file and replace QuickStart with the module name
	// This cover the case where there's a package.json but no src directory.
	if !slices.ContainsFunc(moduleSourceFiles, func(s string) bool {
		return path.Ext(s) == ".ts"
	}) {
		base = base.
			WithDirectory("src", base.Directory("/opt/module/template/src"), ContainerWithDirectoryOpts{Include: []string{"*.ts"}}).
			WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/QuickStart/%s/g", strcase.ToCamel(name)), "src/index.ts"})
	}

	return base, nil
}

// Base returns a Node or Bun container with cache setup for yarn or bun
func (t *TypescriptSdk) Base(runtime SupportedTSRuntime) (*Container, error) {
	switch runtime {
	case Bun:
		return dag.Container().
			From(bunImageRef).
			WithoutEntrypoint().
			WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(fmt.Sprintf("mod-bun-cache-%s", bunVersion))).
			// Add SDK source code before to install its dependencies so we can benefit from the cache
			// and do not reinstall the dependencies each time the codegen is run or something
			// changed in the user file.
			WithDirectory(GenDir, t.SDKSourceDir, ContainerWithDirectoryOpts{
				Exclude: []string{"codegen", "runtime"},
				}).
			WithWorkdir(GenDir).
			WithExec([]string{"bun", "install"}), nil
	case Node:
		return dag.Container().
			From(nodeImageRef).
			WithoutEntrypoint().
			// Install default CA certificates and configure node to use them instead of its compiled in CA bundle.
			// This enables use of custom CA certificates if configured in the dagger engine.
			WithExec([]string{"apk", "add", "ca-certificates"}).
			WithEnvVariable("NODE_OPTIONS", "--use-openssl-ca").
			WithMountedCache("/root/.npm", dag.CacheVolume(fmt.Sprintf("mod-npm-cache-%s", nodeVersion))).
			WithMountedCache("/usr/local/share/.cache", dag.CacheVolume(fmt.Sprintf("mod-yarn-cache-%s", nodeVersion))).
			WithExec([]string{"npm", "install", "-g", "tsx"}).			
			WithDirectory(GenDir, t.SDKSourceDir, ContainerWithDirectoryOpts{
				Exclude: []string{"codegen", "runtime"},
				}).
			WithWorkdir(GenDir).
			WithExec([]string{"yarn", "install", "--production"}), nil
	default:
		return nil, fmt.Errorf("unknown runtime: %s", runtime)
	}
}

// DetectRuntime returns the runtime(bun or node) detected for the user's module
// If a runtime is specfied inside the package.json, it will be used.
// If a package-lock.json, yarn.lock, or pnpm-lock.yaml is present, node will be used.
// If a bun.lockb is present, bun will be used.
// If none of the above is present, node will be used.
func (t *TypescriptSdk) DetectRuntime(ctx context.Context, modSource *ModuleSource, subPath string) (SupportedTSRuntime, error) {
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
				return "", fmt.Errorf("detected unknown runtime: %s", runtime)
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
