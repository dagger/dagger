// Runtime module for the PHP SDK
// when done add php to core/schema/sdk.go:L93

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"php-sdk/internal/dagger"
)

const (
	PhpImage      = "php:8.3-cli-alpine@sha256:e4ffe0a17a6814009b5f0713a5444634a9c5b688ee34b8399e7d4f2db312c3b4"
	ComposerImage = "composer:2@sha256:6d2b5386580c3ba67399c6ccfb50873146d68fcd7c31549f8802781559bed709"
	EntryPoint    = "entrypoint.php"
	ModSourceDir  = "/src"
	GenDir        = "sdk"
)

type PhpSdk struct {
	SourceDir     *dagger.Directory
	RequiredPaths []string
	Container     *dagger.Container
}

func New(
	// Directory with the PHP SDK source code.
	// +optional
	sdkSourceDir *dagger.Directory,
) *PhpSdk {
	if sdkSourceDir == nil {
		sdkRoot := dag.
			CurrentModule().
			Source().
			Directory("../")

		sdkSourceDir = dag.
			Directory().
			WithDirectory("src", sdkRoot.Directory("src")).
			WithDirectory("template", sdkRoot.Directory("template")).
			WithDirectory("generated", sdkRoot.Directory("generated")).
			WithFiles("./", []*dagger.File{
				sdkRoot.File("composer.json"),
				sdkRoot.File("dagger.json"),
				sdkRoot.File(EntryPoint),
				sdkRoot.File("init-template.sh"),
				sdkRoot.File("LICENSE"),
				sdkRoot.File("README.md"),
			})
	}

	return &PhpSdk{
		SourceDir:     sdkSourceDir,
		RequiredPaths: []string{},
		Container:     dag.Container().From(PhpImage),
	}
}

func (sdk *PhpSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	ctr, err := sdk.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return dag.
			GeneratedCode(ctr.Directory(ModSourceDir)).
			WithVCSGeneratedPaths([]string{GenDir + "/**"}).
			WithVCSIgnoredPaths([]string{GenDir}),
		nil
}

func (sdk *PhpSdk) CodegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {

	/**
	 * Mounts  PHP SDK code,
	 * Installs composer into the container
	 * Runs composer install in the codegen directory
	 * Runs codegen using the schema json provided by the dagger engine
	 */
	ctr := sdk.Container.
		WithMountedDirectory("/codegen", sdk.SourceDir).
		WithWorkdir("/codegen").
		WithExec([]string{"apk", "add", "git", "openssh", "curl"})

	ctr = sdk.ComposerInstall(ctr).
		WithMountedFile("schema.json", introspectionJSON).
		WithExec([]string{
			"./" + EntryPoint,
			"dagger:codegen",
			"--schema-file",
			"schema.json",
		})

	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
	}

	/**
	 * Mounts the directory for the module we are generating for
	 * Copies the generated code and rest of the sdk into the module directory under the sdk path
	 * Runs the init template script for initialising a new module (this is a no-op if a composer.json already exists)
	 */

	ctr = ctr.
		WithMountedDirectory(ModSourceDir, modSource.ContextDirectory()).
		WithWorkdir(filepath.Join(ModSourceDir, subPath)).
		WithDirectory(GenDir, ctr.Directory("/codegen"), dagger.
			ContainerWithDirectoryOpts{Exclude: []string{
			EntryPoint,
			"runtime",
			"vendor",
			"schema.json",
		}}).
		WithExec([]string{
			"/codegen/init-template.sh",
			filepath.Join(ModSourceDir, subPath),
			name,
		})

	return ctr, nil
}

func (sdk *PhpSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %w", err)
	}

	ctr, err := sdk.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	ctr = sdk.ComposerInstall(ctr)

	return ctr.WithEntrypoint([]string{filepath.Join(
		ModSourceDir,
		subPath,
		EntryPoint,
	)}), nil
}

func (sdk *PhpSdk) ComposerInstall(
	ctr *dagger.Container,
) *dagger.Container {
	return ctr.
		WithFile(
			"/usr/bin/composer",
			dag.Container().From(ComposerImage).File("/usr/bin/composer"),
		).
		WithExec([]string{"composer", "install"})
}
