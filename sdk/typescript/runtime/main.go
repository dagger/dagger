package main

import (
	"context"
	"fmt"
	"path"

	"github.com/iancoleman/strcase"
)


const (
	bunVersion  = "1.0.27"
	nodeVersion = "21.3"

	nodeImageDigest = "sha256:cacb4e3a208aa34e0f821b56256e446ad984960d4f9aca66c7026e16b87db89f"
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
	EntrypointExecutablePath = "src/" + EntrypointExecutableFile
	sdkSrc                   = "/sdk"
	genDir                   = "sdk"
	genPath                  = "/sdk/api"
	schemaPath               = "/schema.json"
	codegenBinPath           = "/codegen"
)

// ModuleRuntime returns a container with the node entrypoint ready to be called.
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

	entrypointPath := path.Join(ModSourceDirPath, subPath, EntrypointExecutablePath)
	tsConfigPath := path.Join(ModSourceDirPath, subPath, "tsconfig.json")

	ctr = ctr.WithMountedFile(entrypointPath, ctr.Directory("/opt/bin").File(EntrypointExecutableFile))

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

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{
			genDir + "/**",
		}).
		WithVCSIgnoredPaths([]string{
			genDir,
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

	ctr, err := t.Base(detectedRuntime)
	if err != nil {
		return nil, err
	}

	ctr = ctr. // Add sdk directory without runtime nor codegen binary
			WithMountedDirectory(sdkSrc, t.SDKSourceDir).
		// Add codegen binary into a special path
		WithMountedFile(codegenBinPath, t.SDKSourceDir.File("/codegen")).
		// Add template directory
		WithMountedDirectory("/opt", dag.CurrentModule().Source().Directory(".")).
		// Mount users' module
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithWorkdir(path.Join(ModSourceDirPath, subPath)).
		WithNewFile(schemaPath, ContainerWithNewFileOpts{
			Contents: introspectionJson,
		}).
		// Execute the code generator using the given introspection file
		WithExec([]string{
			codegenBinPath,
			"--lang", "typescript",
			"--module-context", ModSourceDirPath,
			"--output", genPath,
			"--module-name", name,
			"--introspection-json-path", schemaPath,
		}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})

	base := ctr.WithDirectory(genDir, ctr.Directory(sdkSrc), ContainerWithDirectoryOpts{
		Exclude: []string{
			"node_modules",
			"dist",
			"codegen",
			"**/test",
			"runtime",
		},
	})

	switch detectedRuntime {
	case Bun:
		base = base.
			// Check if the project has existing source:
			// if it does: add sdk as dev dependency
			// if not: copy the template and replace QuickStart with the module name
			WithExec([]string{"sh", "-c",
				"if [ -f package.json ]; then  bun install ./sdk  --dev  && bun /opt/bin/__tsconfig.updator.ts; else cp -r /opt/template/*.json .; fi",
			},
				ContainerWithExecOpts{SkipEntrypoint: true},
			)
	case Node:
		base = base.
			WithExec([]string{"npm", "install", "-g", "tsx"}).
			// Check if the project has existing source:
			// if it does: add sdk as dev dependency
			// if not: copy the template and replace QuickStart with the module name
			WithExec([]string{"sh", "-c",
				"if [ -f package.json ]; then  npm install --package-lock-only ./sdk  --dev  && tsx /opt/bin/__tsconfig.updator.ts; else cp -r /opt/template/*.json .; fi",
			},
				ContainerWithExecOpts{SkipEntrypoint: true},
			)
	default:
		return nil, fmt.Errorf("unknown runtime: %v", detectedRuntime)
	}

	return base.
		// Check if there's a src directory with .ts files in it.
		// If not, add the template file and replace QuickStart with the module name
		// This cover the case where there's a package.json but no src directory.
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("mkdir -p src && if ls src/*.ts >/dev/null 2>&1; then true; else cp /opt/template/src/index.ts src/index.ts && sed -i -e 's/QuickStart/%s/g' ./src/index.ts; fi", strcase.ToCamel(name))},
			ContainerWithExecOpts{SkipEntrypoint: true}), nil
}

// Base returns a Node container with cache setup for yarn
func (t *TypeScriptSdk) Base(runtime SupportedTSRuntime) (*Container, error) {
	switch runtime {
	case Bun:
		return dag.Container().
			From(bunImageRef).
			WithMountedCache("/root/.bun/install/cache", dag.CacheVolume(fmt.Sprintf("mod-bun-cache-$s", bunVersion))).
			WithoutEntrypoint(), nil
	case Node:
		return dag.Container().
			From(nodeImageRef).
			WithMountedCache("/root/.npm", dag.CacheVolume(fmt.Sprintf("mod-npm-cache-%s", nodeVersion))).
			WithoutEntrypoint(), nil
	default:
		return nil, fmt.Errorf("unknown runtime: %v", runtime)
	}
}

func (t *TypeScriptSdk) DetectRuntime(ctx context.Context, modSource *ModuleSource, subPath string) (SupportedTSRuntime, error) {
	detectedRuntime, err := dag.Container().
		From(bunImageRef).
		WithMountedDirectory("/opt", dag.CurrentModule().Source().Directory(".")).
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithWorkdir(path.Join(ModSourceDirPath, subPath)).
		WithExec([]string{"bun", "/opt/bin/__runtime.detection.ts"}).
		Stdout(ctx)

	if err != nil {
		return "", fmt.Errorf("could not detect runtime: %v", err)
	}

	switch detectedRuntime {
	case string(Bun), string(Node):
		return SupportedTSRuntime(detectedRuntime), nil
	default:
		return "", fmt.Errorf("detected unknown runtime: %v", detectedRuntime)
	}
}
