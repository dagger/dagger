// Runtime module for the PHP SDK
// when done add php to core/schema/sdk.go:L93

package main

import (
	"context"
	"fmt"
	"path/filepath"
)

const (
	DefaultImage          = "php:8.3-cli-alpine"
	ModSourceDirPath      = "/src"
	RuntimeExecutablePath = "dagger"
	GenDir                = "sdk"
)

type PhpSdk struct {
	SourceDir     *Directory
	RequiredPaths []string
	Container     *Container
}

func New(
	// Directory with the PHP SDK source code.
	// +optional
	sdkSourceDir *Directory,
) *PhpSdk {
	if sdkSourceDir == nil {
		// TODO: change back to git or something remote when the runtime is ready.
		// Go back to ../ to get the root of the PHP SDK
		// This is a bit of a hack, you need to call it from a parent directory but it works.
		// Example from ../../ (the directory above the dagger git repo)
		// dagger init --sdk=dagger/sdk/php --source=test
		sdkSourceDir = dag.CurrentModule().Source().Directory("../").WithoutDirectory("runtime")
	}

	return &PhpSdk{
		SourceDir:     sdkSourceDir,
		RequiredPaths: []string{},
		Container:     dag.Container().From(DefaultImage),
	}
}

func (sdk *PhpSdk) Codegen(ctx context.Context, modSource *ModuleSource, introspectionJSON string) (*GeneratedCode, error) {
	ctr, err := sdk.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
			WithVCSGeneratedPaths([]string{"/codegen/generated" + "/**"}).
			WithVCSIgnoredPaths([]string{"/codegen/generated"}),
		nil
}

func (sdk *PhpSdk) CodegenBase(ctx context.Context, modSource *ModuleSource, introspectionJSON string) (*Container, error) {
	ctr := sdk.Container

	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
	}

	/**
	 * Mounts the PHP SDK code,
	 * Installs composer into the container
	 * Runs composer install in the codegen directory
	 * Runs codegen using the schema json provided by the dagger engine
	 */
	ctr = ctr.
		WithMountedDirectory("/codegen", sdk.SourceDir).
		WithoutEntrypoint().
		WithWorkdir("/codegen").
		WithExec([]string{
		  "apk", "add", "git", "openssh", "curl",
		}).
		WithExec([]string {
		  "git", "config", "--global", "url.https://github.com/.insteadOf", "git@github.com:",
		}).
		WithExec([]string{
			"./install-composer.sh",
		}).
		WithExec([]string{
			"php", "composer.phar", "install",
		}).
		WithNewFile("schema.json", ContainerWithNewFileOpts{
			Contents: introspectionJSON,
		}).
		WithExec([]string{
			"./codegen", "dagger:codegen", "--schema-file", "schema.json",
		})

	/**
	 * Mounts the directory for the module we are generating for
	 * Copies the generated code and rest of the sdk into the module directory under the sdk path
	 * Runs the init template script for initialising a new module (this is a no-op if a composer.json already exists)
	 */
	ctr = ctr.
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithWorkdir(filepath.Join(ModSourceDirPath, subPath)).
		WithDirectory(GenDir, ctr.Directory("/codegen"), ContainerWithDirectoryOpts{
			Exclude: []string{
				"codegen",
				"runtime",
				"docker",
				"docker-compose.yml",
				".changes",
				".changie.yaml",
				"vendor",
				"tests",
				"phpunit.xml.dist",
				"psalm.xml",
				".php-cs-fixer.dist.php",
				"install-composer.sh",
				"composer.phar",
				"composer.lock",
				"schema.json",
			},
		}).
		WithExec([]string{
			"/codegen/init-template.sh", filepath.Join(ModSourceDirPath, subPath), name,
		}).
		WithFile("./install-composer.sh", ctr.File("/codegen/install-composer.sh"))

	return ctr, nil
}

func (sdk *PhpSdk) ModuleRuntime(ctx context.Context, modSource *ModuleSource, introspectionJSON string) (*Container, error) {
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
	}

	ctr, err := sdk.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	ctr = ctr.
		WithExec([]string{
			"./install-composer.sh",
		}).
		WithExec([]string{
			"php", "composer.phar", "install",
		})

	filepath.Join(ModSourceDirPath, subPath, RuntimeExecutablePath)

	return ctr.WithEntrypoint([]string{filepath.Join(ModSourceDirPath, subPath, "dagger")}), nil
}
