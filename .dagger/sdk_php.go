package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

const (
	phpSDKImage         = "php:8.3-cli-alpine"
	phpSDKDigest        = "sha256:e4ffe0a17a6814009b5f0713a5444634a9c5b688ee34b8399e7d4f2db312c3b4"
	phpSDKComposerImage = "composer:2@sha256:6d2b5386580c3ba67399c6ccfb50873146d68fcd7c31549f8802781559bed709"
	phpSDKVersionFile   = "src/Connection/version.php"

	phpDoctumVersion = "5.5.4"
)

type PHPSDK struct {
	OriginalWorkspace *dagger.Directory // +private
	Workspace         *dagger.Directory // +private
	DoctumConfigPath  string            // +private
	SourcePath        string            // +private
	BaseContainer     *dagger.Container // +private
}

// Develop the Dagger PHP SDK (experimental)
func (sdks *SDK) PHP(
	// A directory with all the files needed to develop the SDK
	// +defaultPath="/"
	// +ignore=["*", "!sdk/php", "sdk/php/vendor", "!docs/doctum-config.php"]
	workspace *dagger.Directory,
	// The path of the SDK source in the workspace
	// +default="sdk/php"
	sourcePath string,
	// The path of the doctum config in the workspace
	// +default="docs/doctum-config.php"
	doctumConfigPath string,
) *PHPSDK {
	// Extract the PHP base container from the native SDK dev module
	// - We build the base container eagerly, to avoid keeping a reference to DaggerDev
	// - But we build the full dev container *lazily*, because we may have mutated our workspace with generated files
	baseContainer := dag.
		PhpSDKDev(dagger.PhpSDKDevOpts{Source: nil}).
		Base().
		With(sdks.Dagger.devEngineSidecar())
	return &PHPSDK{
		Workspace:         workspace,
		OriginalWorkspace: workspace,
		SourcePath:        sourcePath,
		DoctumConfigPath:  doctumConfigPath,
		BaseContainer:     baseContainer,
	}
}

// Returns the PHP SDK workspace mounted in a dev container,
// and working directory set to the SDK source
func (t PHPSDK) DevContainer() *dagger.Container {
	return t.BaseContainer.
		WithMountedDirectory(".", t.Workspace).
		WithWorkdir(t.SourcePath)
}

// Self-call PHP() without losing the default values
// FIXME: this is needed for the allsdk[] plumbing:
// aggregating the same function call across all SDKs. Currently used by:
//   - dagger call bump
//   - dagger call generate
//   - ... and various stopgap check functions, soon to be removed
//
// --> 'bump' and 'generate' remain a problem
func (sdks *SDK) selfCallPHP() *PHPSDK {
	return sdks.PHP(
		// workspace
		sdks.Dagger.Source.Filter(dagger.DirectoryFilterOpts{
			Include: []string{"sdk/php", "docs/doctum-config.php"},
		}),
		// sourcePath
		"sdk/php",
		// doctumConfigPath
		"docs/doctum-config.php",
	)
}

// Source returns the source directory for the PHP SDK
func (t PHPSDK) Source() *dagger.Directory {
	return t.Workspace.Directory(t.SourcePath)
}

// DoctumConfig returns the doctum configuration file
func (t PHPSDK) DoctumConfig() *dagger.File {
	return t.Workspace.File(t.DoctumConfigPath)
}

func (t PHPSDK) Name() string {
	return "php"
}

// Lint the PHP code with PHP CodeSniffer (https://github.com/squizlabs/PHP_CodeSniffer)
func (t PHPSDK) PhpCodeSniffer(ctx context.Context) (MyCheckStatus, error) {
	_, err := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Source: t.Source()}).
		Lint().
		Sync(ctx)
	return CheckCompleted, err
}

// Analyze the PHP code with PHPStan (https://phpstan.org)
func (t PHPSDK) PhpStan(ctx context.Context) (MyCheckStatus, error) {
	_, err := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Source: t.Source()}).
		Analyze().
		Sync(ctx)
	return CheckCompleted, err
}

// Test the PHP SDK
func (t PHPSDK) Test(ctx context.Context) (MyCheckStatus, error) {
	base := t.DevContainer().
		WithEnvVariable("PATH", "./vendor/bin:$PATH", dagger.ContainerWithEnvVariableOpts{Expand: true})

	dev := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Container: base, Source: t.Source()})
	_, err := dev.Test().Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	return CheckCompleted, nil
}

// Regenerate the PHP SDK API + docs
func (t *PHPSDK) Generate(ctx context.Context) (*dagger.Changeset, error) {
	t, err := t.
		WithGeneratedClient().
		WithGeneratedDocs(ctx)
	if err != nil {
		return nil, err
	}
	return t.Changes(), nil
}

func (t *PHPSDK) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t *PHPSDK) WithGeneratedClient() *PHPSDK {
	relLayer := t.DevContainer().
		WithoutDirectory("generated").
		WithDirectory("generated", dag.Directory()).
		// FIXME: why not inject the right dagger binary, instead of leaking this env var?
		WithExec([]string{"sh", "-c", "$_EXPERIMENTAL_DAGGER_CLI_BIN run ./scripts/codegen.php"}).
		Directory(".").
		Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				"vendor",
			},
		})
	t.Workspace = t.Workspace.
		WithoutDirectory("sdk/php").
		WithDirectory("sdk/php", relLayer)
	return t
}

// Generate reference docs from the generated client
// NOTE: it's the caller's responsibility to ensure the generated client is up-to-date
// (see WithGeneratedClient)
func (t *PHPSDK) WithGeneratedDocs(ctx context.Context) (*PHPSDK, error) {
	relLayer := t.DevContainer().
		WithFile(
			"/usr/bin/doctum",
			dag.HTTP(fmt.Sprintf("https://doctum.long-term.support/releases/%s/doctum.phar", phpDoctumVersion)),
			dagger.ContainerWithFileOpts{Permissions: 0711},
		).
		WithFile("/etc/doctum-config.php", t.DoctumConfig()).
		WithExec([]string{"doctum", "update", "/etc/doctum-config.php", "-v"}).
		Directory("/src/sdk/php/build")

	// format this file, since otherwise it's on one line and makes lots of conflicts
	// FIXME: use dagger JSON API
	search, err := formatJSONFile(ctx, relLayer.File("doctum-search.json"))
	if err != nil {
		return nil, err
	}
	relLayer = relLayer.
		WithFile("doctum-search.json", search).
		// remove the renderer.index file, which seems to not be required to render the docs
		WithoutFile("renderer.index")
	t.Workspace = t.Workspace.
		WithoutDirectory("docs/static/reference/php/").
		WithDirectory("docs/static/reference/php/", relLayer)
	return t, nil
}

// Test the publishing process
func (t PHPSDK) ReleaseDryRun(
	ctx context.Context,
	// The git repository to publish *from*
	// +defaultPath="/"
	fromRepo *dagger.GitRepository,
) (MyCheckStatus, error) {
	return CheckCompleted, t.Publish(ctx, fromRepo, "HEAD", true, "https://github.com/dagger/dagger-php-sdk.git", nil)
}

// Publish the PHP SDK
func (t PHPSDK) Publish(
	ctx context.Context,

	// The git repository to publish *from*
	// +defaultPath="/"
	fromRepo *dagger.GitRepository,

	tag string,

	// +optional
	dryRun bool,

	// +optional
	// +default="https://github.com/dagger/dagger-php-sdk.git"
	gitRepo string,
	// +optional
	githubToken *dagger.Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/php/")

	if err := gitPublish(ctx, fromRepo, gitPublishOpts{
		sdk:         "php",
		sourcePath:  "sdk/php/",
		sourceTag:   tag,
		dest:        gitRepo,
		destTag:     version,
		githubToken: githubToken,
		dryRun:      dryRun,
	}); err != nil {
		return err
	}

	return nil
}

// Bump the PHP SDK's Engine dependency
func (t PHPSDK) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	version = strings.TrimPrefix(version, "v")
	content := fmt.Sprintf(
		"<?php\n\n/* Code generated by dagger. DO NOT EDIT. */\nreturn '%s';\n",
		version,
	)

	layer := dag.Directory().WithNewFile(filepath.Join("sdk/php", phpSDKVersionFile), content)
	return layer.Changes(dag.Directory()).Sync(ctx)
}
