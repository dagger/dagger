package main

import (
	"context"
	"fmt"
	"github.com/iancoleman/strcase"
	"os"
	"path"
	"path/filepath"
)

type TypescriptSdk struct{}

const (
	ModSourceDirPath         = "/src"
	EntrypointExecutablePath = "sdk/entrypoint/entrypoint.ts"
	sdkSrc                   = "/sdk"
	genDir                   = "sdk"
	genPath                  = "/sdk/api"
	schemaPath               = "/schema.json"
	codegenBinPath           = "/codegen"
)

// ModuleRuntime returns a container with the node entrypoint ready to be called.
func (t *TypescriptSdk) ModuleRuntime(ctx context.Context, modSource *Directory, subPath string, introspectionJson string) *Container {
	return t.
		CodegenBase(ctx, modSource, subPath, introspectionJson).
		// Install dependencies
		WithExec([]string{"yarn", "install"}).
		// Add tsx to execute the entrypoint
		WithExec([]string{"yarn", "global", "add", "tsx"}).
		WithWorkdir(ModSourceDirPath).
		WithEntrypoint([]string{"tsx", EntrypointExecutablePath}).
		WithDefaultArgs()
}

// Codegen returns the generated API client based on user's module
func (t *TypescriptSdk) Codegen(ctx context.Context, modSource *Directory, subPath string, introspectionJson string) *GeneratedCode {
	// Get base container
	ctr := t.CodegenBase(ctx, modSource, subPath, introspectionJson)

	// Add the SDK source into the container at genDir path
	ctr = ctr.WithDirectory(genDir, ctr.Directory(sdkSrc), ContainerWithDirectoryOpts{
		Exclude: []string{
			"node_modules",
			"dist",
			"codegen",
			"**/test",
			"runtime",
		},
	})

	// Compare difference to improve performances
	modified := ctr.Directory(ModSourceDirPath)
	diff := modSource.Diff(modified).Directory(subPath)

	// Return the difference and fill .gitignore
	return dag.GeneratedCode(diff).
		WithVCSIgnoredPaths([]string{
			genDir,
			"node_modules",
		})
}

// CodegenBase returns a Container containing the SDK from the engine container
// and the user's code with a generated API based on what he did.
func (t *TypescriptSdk) CodegenBase(ctx context.Context, modSource *Directory, subPath string, introspectionJson string) *Container {
	// Load module name for the template class
	name, err := dag.ModuleConfig(modSource).Name(ctx)
	if err != nil {
		panic(fmt.Errorf("could not load module config: %v", err))
	}

	return t.Base("").
		// Add sdk directory without runtime nor codegen binary
		WithDirectory(sdkSrc, dag.Host().Directory(root(), HostDirectoryOpts{
			Exclude: []string{"runtime, codegen"},
		})).
		// Add codegen binary into a special path
		WithFile(codegenBinPath, dag.Host().File("/src/codegen")).
		// Add template directory
		WithDirectory("/opt", dag.Host().Directory(root(), HostDirectoryOpts{
			Include: []string{"runtime/template"},
		})).
		// Mount users' module
		WithMountedDirectory(ModSourceDirPath, modSource).
		WithWorkdir(path.Join(ModSourceDirPath, subPath)).
		WithNewFile(schemaPath, ContainerWithNewFileOpts{
			Contents: introspectionJson,
		}).
		// Execute the code generator using the given introspection file
		WithExec([]string{
			codegenBinPath,
			"--lang", "nodejs",
			"--output", genPath,
			"--introspection-json-path", schemaPath,
		}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			SkipEntrypoint:                true,
		}).
		// If it's an init, add the template and replace the QuickStart class name
		// with the user's module name
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("[ -f package.json ] || cp -r /opt/runtime/template/* . && sed -i -e 's/QuickStart/%s/g' ./src/index.ts", strcase.ToCamel(name))},
			ContainerWithExecOpts{SkipEntrypoint: true},
		)
}

// Base returns a Node container with cache setup for yarn
func (t *TypescriptSdk) Base(version string) *Container {
	if version == "" {
		version = "21.3-alpine"
	}

	return dag.Container().
		From(fmt.Sprintf("node:%s", version)).
		WithMountedCache("/usr/local/share/.cache/yarn", dag.CacheVolume("yarn-cache"))
}

// TODO: fix .. restriction
func root() string {
	workdir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	return filepath.Join(workdir, "..")
}
