package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/iancoleman/strcase"
)

type TypeScriptSdk struct{}

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
func (t *TypeScriptSdk) ModuleRuntime(ctx context.Context, modSource *Directory, subPath string, introspectionJson string) (*Container, error) {
	ctr, err := t.CodegenBase(ctx, modSource, subPath, introspectionJson)
	if err != nil {
		return nil, err
	}

	return ctr.
		// Install dependencies
		WithExec([]string{"npm", "install"}).
		WithMountedFile(EntrypointExecutablePath, ctr.Directory("/opt/runtime/bin").File(EntrypointExecutableFile)).
		WithEntrypoint([]string{"tsx", EntrypointExecutablePath}), nil
}

// Codegen returns the generated API client based on user's module
func (t *TypeScriptSdk) Codegen(ctx context.Context, modSource *Directory, subPath string, introspectionJson string) (*GeneratedCode, error) {
	// Get base container
	ctr, err := t.CodegenBase(ctx, modSource, subPath, introspectionJson)
	if err != nil {
		return nil, err
	}

	// Compare difference to improve performances
	modified := ctr.Directory(ModSourceDirPath)
	diff := modSource.Diff(modified).Directory(subPath)

	// Return the difference and fill .gitignore
	return dag.GeneratedCode(diff).
		WithVCSIgnoredPaths([]string{
			genDir,
			"node_modules",
		}), nil
}

// CodegenBase returns a Container containing the SDK from the engine container
// and the user's code with a generated API based on what he did.
func (t *TypeScriptSdk) CodegenBase(ctx context.Context, modSource *Directory, subPath string, introspectionJson string) (*Container, error) {
	// Load module name for the template class
	name, err := dag.ModuleConfig(modSource.Directory(subPath)).Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	ctr := t.Base("", name).
		// Add sdk directory without runtime nor codegen binary
		WithDirectory(sdkSrc, dag.Host().Directory(root(), HostDirectoryOpts{
			Exclude: []string{"runtime", "codegen"},
		})).
		// Add codegen binary into a special path
		WithFile(codegenBinPath, dag.Host().File("/src/codegen")).
		// Add template directory
		WithDirectory("/opt", dag.Host().Directory(root(), HostDirectoryOpts{
			Include: []string{"runtime/template", "runtime/bin"},
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
			"--lang", "typescript",
			"--output", genPath,
			"--introspection-json-path", schemaPath,
		}, ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})

	// Add SDK src to the generated directory
	ctr = ctr.WithDirectory(genDir, ctr.Directory(sdkSrc), ContainerWithDirectoryOpts{
		Exclude: []string{
			"node_modules",
			"dist",
			"codegen",
			"**/test",
			"runtime",
		},
	})

	ctr = ctr.
		// Add tsx to execute the entrypoint
		WithExec([]string{"npm", "install", "-g", "tsx"}).
		// Check if the project has existing source:
		// if it does: add sdk as dev dependency
		// if not: copy the template and replace QuickStart with the module name
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("if [ -f package.json ]; then  npm install --package-lock-only ./sdk  --dev  && tsx /opt/runtime/bin/__tsconfig.updator.ts; else cp -r /opt/runtime/template/*.json /opt/runtime/template/yarn.lock  .; fi")},
			ContainerWithExecOpts{SkipEntrypoint: true},
		).
		// Check if there's a src directory with .ts files in it.
		// If not, add the template file and replace QuickStart with the module name
		// This cover the case where there's a package.json but no src directory.
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("mkdir -p src && if ls src/*.ts >/dev/null 2>&1; then true; else cp /opt/runtime/template/src/index.ts src/index.ts && sed -i -e 's/QuickStart/%s/g' ./src/index.ts; fi", strcase.ToCamel(name))}, 
			ContainerWithExecOpts{SkipEntrypoint: true})

	return ctr, nil
}

// Base returns a Node container with cache setup for yarn
func (t *TypeScriptSdk) Base(version string, pkgName string) *Container {
	if version == "" {
		version = "21.3-alpine"
	}

	return dag.Container().
		From(fmt.Sprintf("node:%s", version)).
		WithMountedCache("/root/.npm", dag.CacheVolume("mod-npm-cache-"+version+"-"+pkgName)).
		WithoutEntrypoint()
}

// TODO: fix .. restriction
func root() string {
	workdir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	return filepath.Join(workdir, "..")
}
