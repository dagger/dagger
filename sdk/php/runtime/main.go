// Runtime module for the PHP SDK

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"php-sdk/internal/dagger"
)

const (
	PhpImage      = "php:8.3-cli-alpine"
	PhpDigest     = "sha256:e4ffe0a17a6814009b5f0713a5444634a9c5b688ee34b8399e7d4f2db312c3b4"
	ComposerImage = "composer:2@sha256:6d2b5386580c3ba67399c6ccfb50873146d68fcd7c31549f8802781559bed709"
	ModSourcePath = "/src"
	GenPath       = "sdk"
)

type PhpSdk struct {
	SourceDir     *dagger.Directory
	RequiredPaths []string
}

func New(
	// Directory with the PHP SDK source code.
	// +optional
	// +defaultPath="/sdk/php"
	// +ignore=["**", "!generated/", "!src/", "!scripts/", "!composer.json", "!composer.lock", "!LICENSE", "!README.md"]
	sdkSourceDir *dagger.Directory,
) (*PhpSdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &PhpSdk{
		RequiredPaths: []string{},
		SourceDir:     sdkSourceDir,
	}, nil
}

func (m *PhpSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	return dag.
		GeneratedCode(ctr.Directory(ModSourcePath)).
		WithVCSGeneratedPaths([]string{
			GenPath + "/**",
			"entrypoint.php",
		}).
		WithVCSIgnoredPaths([]string{GenPath, "vendor"}), nil
}

func (m *PhpSdk) CodegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module name: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module source path: %w", err)
	}

	base := dag.Container().
		From(fmt.Sprintf("%s@%s", PhpImage, PhpDigest)).
		WithExec([]string{"apk", "add", "git", "openssh", "curl"}).
		WithFile("/usr/bin/composer", dag.Container().From(ComposerImage).File("/usr/bin/composer")).
		WithMountedCache("/root/.composer", dag.CacheVolume(fmt.Sprintf("composer-%s", PhpImage))).
		WithEnvVariable("COMPOSER_HOME", "/root/.composer").
		WithEnvVariable("COMPOSER_NO_INTERACTION", "1").
		WithEnvVariable("COMPOSER_ALLOW_SUPERUSER", "1")

	/**
	 * Mounts PHP SDK code and installs it
	 * Runs codegen using the schema json provided by the dagger engine
	 */
	ctr := base.
		WithDirectory("/sdk", m.SourceDir).
		WithWorkdir("/sdk").
		// Needed to run codegen
		WithExec([]string{"composer", "install"})

	sdkDir := ctr.
		WithMountedFile("/schema.json", introspectionJSON).
		WithExec([]string{
			"scripts/codegen.php",
			"dagger:codegen",
			"--schema-file",
			"/schema.json",
		}).
		WithoutDirectory("vendor").
		WithoutDirectory("scripts").
		WithoutFile("composer.lock").
		Directory(".")

	srcPath := filepath.Join(ModSourcePath, subPath)
	sdkPath := filepath.Join(srcPath, GenPath)
	runtime := dag.CurrentModule().Source()

	ctxDir := modSource.ContextDirectory().
		// Just in case the user didn't add these to the module's
		// dagger.json exclude list.
		WithoutDirectory(filepath.Join(subPath, "vendor")).
		WithoutDirectory(filepath.Join(subPath, GenPath))

	/**
	 * Mounts the directory for the module we are generating for
	 * Copies the generated code and rest of the sdk into the module directory under the sdk path
	 * Runs the init template script for initialising a new module (this is a no-op if a composer.json already exists)
	 */
	ctr = ctr.
		WithMountedDirectory("/opt/template", runtime.Directory("template")).
		WithMountedFile("/init-template.sh", runtime.File("scripts/init-template.sh")).
		WithMountedDirectory(ModSourcePath, ctxDir).
		WithDirectory(sdkPath, sdkDir).
		WithWorkdir(srcPath).
		WithExec([]string{"/init-template.sh", name}).
		// composer install adds the lock file so we want this even in Codegen.
		WithExec([]string{"composer", "install"}).
		WithEntrypoint([]string{filepath.Join(srcPath, "entrypoint.php")})

	return ctr, nil
}

func (m *PhpSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	// We could just move CodegenBase to ModuleRuntime, but keeping them
	// separate allows for easier future changes.
	return m.CodegenBase(ctx, modSource, introspectionJSON)
}
