// Runtime module for the PHP SDK

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"php-sdk/internal/dagger"
)

const (
	PhpImage       = "php:8.4-cli-alpine" // TODO: because of how defaults works, this is not used. Should I switch to `nil` by default and fallback to using those constants ?
	PhpDigest      = "sha256:fada271bbcae269b0b5f93212432a70ffb0aa51de0fa9c925455e8a1afae65ca"
	CurlImage      = "alpine/curl:8.14.1"
	CurlDigest     = "sha256:4007cdf991c197c3412b5af737a916a894809273570b0c2bb93d295342fc23a2"
	ComposerImage  = "composer/composer:2-bin@sha256:73bf0499280eef8014f37daf5c4fb503f7964d9e5d53d447ff26d01d8a7e5d23"
	PieReleasePhar = "https://github.com/php/pie/releases/download/1.2.0/pie.phar"
	PieDigest      = "sha256:5ea836df7244a05d62b300a2294b5b6ae10c951f4f6a5e0d2ae2de84541142f0" // TODO: not sure how to check this after curl
	ModSourcePath  = "/src"
	GenPath        = "sdk"
)

type PhpSdk struct {
	SourceDir *dagger.Directory
	// Set custom php version to use with this module
	PhpVersion string
	// If true will use pie to install extra php modules from composer.json
	UsePie bool
	// List of extension to install through `docker-php-ext-install`
	PhpExtInstall []string
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
		SourceDir: sdkSourceDir,
	}, nil
}

func (m *PhpSdk) WithConfig(
// +default="8.4-cli-alpine@sha256:fada271bbcae269b0b5f93212432a70ffb0aa51de0fa9c925455e8a1afae65ca"
	phpVersion string,
// +default=false
	usePie bool,
// +default=[]string{}
	phpExtInstall []string,
) *PhpSdk {
	m.PhpVersion = phpVersion
	m.UsePie = usePie
	m.PhpExtInstall = phpExtInstall
	return m
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
		From(fmt.Sprintf("php:%s", m.PhpVersion)).
		WithExec([]string{"apk", "add", "git", "openssh", "curl", "jq"}).
		WithFile("/usr/bin/composer", dag.Container().From(ComposerImage).File("/composer")).
		WithMountedCache("/root/.composer", dag.CacheVolume(fmt.Sprintf("composer-%s", m.PhpVersion))).
		WithEnvVariable("COMPOSER_HOME", "/root/.composer").
		WithEnvVariable("COMPOSER_NO_INTERACTION", "1").
		WithEnvVariable("COMPOSER_ALLOW_SUPERUSER", "1")

	if len(m.PhpExtInstall) > 0 {
		for _, ext := range m.PhpExtInstall {
			base = base.
				WithExec([]string{"docker-php-ext-install", ext})
		}
	}

	if m.UsePie {
		base = base.
			WithExec([]string{"apk", "add", "gcc", "make", "autoconf", "libtool", "bison", "re2c", "pkgconf"}). // For php/pie
			WithFile("/usr/bin/pie",
				dag.Container().From(fmt.Sprintf("%s@%s", CurlImage, CurlDigest)).
					WithExec([]string{"curl", "-o", "/usr/bin/pie", "-sLO", PieReleasePhar}).
					WithExec([]string{"chmod", "+x", "/usr/bin/pie"}).
					File("/usr/bin/pie"))
	}

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
		WithEntrypoint([]string{filepath.Join(srcPath, "entrypoint.php")})

	entries, err := ctr.Directory(srcPath).Entries(ctx)
	if err != nil {
		return nil, err
	}

	if slices.Contains(entries, "composer.lock") {
		ctr = ctr.WithExec([]string{
			"composer",
			"update",
			"--with-all-dependencies",
			"--minimal-changes",
			"dagger/dagger"})
	} else {
		ctr = ctr.WithExec([]string{
			"composer",
			"install"})
	}

	if m.UsePie {
		ctr = ctr.WithExec([]string{
			"pie",
			"install",
			"--no-interaction"})
	}

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
