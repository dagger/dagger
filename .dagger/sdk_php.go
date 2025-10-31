package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

const (
	phpSDKImage         = "php:8.3-cli-alpine"
	phpSDKDigest        = "sha256:e4ffe0a17a6814009b5f0713a5444634a9c5b688ee34b8399e7d4f2db312c3b4"
	phpSDKComposerImage = "composer:2@sha256:6d2b5386580c3ba67399c6ccfb50873146d68fcd7c31549f8802781559bed709"
	phpSDKVersionFile   = "src/Connection/version.php"

	phpDoctumVersion = "5.5.4"
)

type PHPSDK struct {
	Dagger *DaggerDev // +private
}

func (t PHPSDK) Name() string {
	return "php"
}

func (t PHPSDK) Source() *dagger.Directory {
	return t.Dagger.Source.Directory("sdk/php")
}

func (t PHPSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("PHP CodeSniffer", func(ctx context.Context) error {
			_, err := t.PhpCodeSniffer(ctx)
			return err
		}).
		WithJob("PHPStan", func(ctx context.Context) error {
			_, err := t.PhpStan(ctx)
			return err
		}).
		Run(ctx)
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
	base := dag.PhpSDKDev().Base().
		With(t.Dagger.devEngineSidecar()).
		WithEnvVariable("PATH", "./vendor/bin:$PATH", dagger.ContainerWithEnvVariableOpts{Expand: true})

	dev := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Container: base, Source: t.Source()})
	_, err := dev.Test().Sync(ctx)
	if err != nil {
		return CheckCompleted, err
	}
	return CheckCompleted, nil
}

// Regenerate the PHP SDK API + docs
func (t PHPSDK) Generate(ctx context.Context) (*dagger.Changeset, error) {
	genClient := t.generateClient()
	genDocs, err := t.generateDocs(ctx, genClient)
	if err != nil {
		return nil, err
	}
	src := t.Dagger.Source
	return src.
		WithChanges(genClient).
		WithChanges(genDocs).
		Changes(src).
		Sync(ctx)
}

func (t PHPSDK) generateClient() *dagger.Changeset {
	src := t.Source()
	relLayer := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Source: src}).
		Base().
		With(t.Dagger.devEngineSidecar()).
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
	// Make the change relative to the repo root
	absLayer := t.Dagger.Source.
		WithoutDirectory("sdk/php").
		WithDirectory("sdk/php", relLayer)
	return absLayer.Changes(t.Dagger.Source)
}

func (t PHPSDK) generateDocs(ctx context.Context, genClient *dagger.Changeset) (*dagger.Changeset, error) {
	// FXME: do we even need the rest of the source?
	src := t.Source().WithChanges(genClient)
	relLayer := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Source: src}).
		Base().
		WithFile(
			"/usr/bin/doctum",
			dag.HTTP(fmt.Sprintf("https://doctum.long-term.support/releases/%s/doctum.phar", phpDoctumVersion)),
			dagger.ContainerWithFileOpts{Permissions: 0711},
		).
		WithFile("/etc/doctum-config.php", t.doctumConfig()).
		WithExec([]string{"doctum", "update", "/etc/doctum-config.php", "-v"}).
		Directory("/src/sdk/php/build")

	// format this file, since otherwise it's on one line and makes lots of conflicts
	search, err := formatJSONFile(ctx, relLayer.File("doctum-search.json"))
	if err != nil {
		return nil, err
	}
	relLayer = relLayer.
		WithFile("doctum-search.json", search).
		// remove the renderer.index file, which seems to not be required to render the docs
		WithoutFile("renderer.index")
	absLayer := t.Dagger.Source.
		WithoutDirectory("docs/static/reference/php/").
		WithDirectory("docs/static/reference/php/", relLayer)
	return absLayer.Changes(t.Dagger.Source), nil
}

// Return the doctum config file from the dagger repo
func (t PHPSDK) doctumConfig() *dagger.File {
	return t.Dagger.Source.File("docs/doctum-config.php")
}

// Test the publishing process
func (t PHPSDK) ReleaseDryRun(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, t.Publish(ctx, "HEAD", true, "https://github.com/dagger/dagger-php-sdk.git", nil)
}

// Publish the PHP SDK
func (t PHPSDK) Publish(
	ctx context.Context,
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

	if err := gitPublish(ctx, t.Dagger.Git, gitPublishOpts{
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
