package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

const (
	phpSDKPath          = "sdk/php"
	phpSDKImage         = "php:8.3-cli-alpine"
	phpSDKDigest        = "sha256:e4ffe0a17a6814009b5f0713a5444634a9c5b688ee34b8399e7d4f2db312c3b4"
	phpSDKComposerImage = "composer:2@sha256:6d2b5386580c3ba67399c6ccfb50873146d68fcd7c31549f8802781559bed709"
	phpSDKGeneratedDir  = "generated"
	phpSDKVersionFile   = "src/Connection/version.php"

	phpDoctumVersion       = "5.5.4"
	phpSDKGeneratedDocsDir = "docs/static/reference/php/"
)

type PHPSDK struct {
	Dagger *DaggerDev // +private
}

// Lint the PHP SDK
func (t PHPSDK) Lint(ctx context.Context) error {
	eg := errgroup.Group{}

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint the php source")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		src := t.Dagger.Source.Directory(phpSDKPath)
		_, err := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Source: src}).Lint().Sync(ctx)
		return err
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that the generated client library is up-to-date")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		before := t.Dagger.Source
		after, err := t.Generate(ctx)
		if err != nil {
			return err
		}
		return dag.Dirdiff().AssertEqual(ctx, before, after, []string{
			filepath.Join(phpSDKPath, phpSDKGeneratedDir),
			phpSDKGeneratedDocsDir,
		})
	})

	return eg.Wait()
}

// Test the PHP SDK
func (t PHPSDK) Test(ctx context.Context) error {
	installer := t.Dagger.installer("sdk")
	src := t.Dagger.Source.Directory(phpSDKPath)
	base := dag.PhpSDKDev().Base().
		With(installer).
		WithEnvVariable("PATH", "./vendor/bin:$PATH", dagger.ContainerWithEnvVariableOpts{Expand: true})

	dev := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Container: base, Source: src})
	_, err := dev.Test().Sync(ctx)
	return err
}

// Regenerate the PHP SDK API + docs
func (t PHPSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	src := t.Dagger.Source.Directory(phpSDKPath)

	api := t.generateAPI(src)

	// NB: we need to chain the API generation into the docs generation, since
	// doctum analyzes the php code to generate docs
	docs, err := t.generateDocs(ctx, src.WithDirectory("", api))
	if err != nil {
		return nil, err
	}

	return dag.Directory().
		WithDirectory("", api).
		WithDirectory("", docs), nil
}

func (t PHPSDK) generateAPI(src *dagger.Directory) *dagger.Directory {
	installer := t.Dagger.installer("sdk")
	generated := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Source: src}).
		Base().
		With(installer).
		WithoutDirectory(phpSDKGeneratedDir).
		WithDirectory(phpSDKGeneratedDir, dag.Directory()).
		WithExec([]string{"sh", "-c", "$_EXPERIMENTAL_DAGGER_CLI_BIN run ./scripts/codegen.php"}).
		Directory(".")
	return dag.Directory().WithDirectory(phpSDKPath, generated)
}

func (t PHPSDK) generateDocs(ctx context.Context, src *dagger.Directory) (*dagger.Directory, error) {
	dir := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Source: src}).
		Base().
		WithFile(
			"/usr/bin/doctum",
			dag.HTTP(fmt.Sprintf("https://doctum.long-term.support/releases/%s/doctum.phar", phpDoctumVersion)),
			dagger.ContainerWithFileOpts{Permissions: 0711},
		).
		WithFile("/etc/doctum-config.php", t.Dagger.Source.File("docs/doctum-config.php")).
		WithExec([]string{"doctum", "update", "/etc/doctum-config.php", "-v"}).
		Directory("/src/sdk/php/build")

	// format this file, since otherwise it's on one line and makes lots of conflicts
	search, err := formatJSONFile(ctx, dir.File("doctum-search.json"))
	if err != nil {
		return nil, err
	}
	dir = dir.WithFile("doctum-search.json", search)

	// remove the renderer.index file, which seems to not be required to render the docs
	dir = dir.WithoutFile("renderer.index")

	return dag.Directory().WithDirectory(phpSDKGeneratedDocsDir, dir), nil
}

// Test the publishing process
func (t PHPSDK) TestPublish(ctx context.Context, tag string) error {
	return t.Publish(ctx, tag, true, "https://github.com/dagger/dagger-php-sdk.git", "https://github.com/dagger/dagger.git", "dagger-ci", "hello@dagger.io", nil)
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
	// +default="https://github.com/dagger/dagger.git"
	gitRepoSource string,
	// +optional
	// +default="dagger-ci"
	gitUserName string,
	// +optional
	// +default="hello@dagger.io"
	gitUserEmail string,

	// +optional
	githubToken *dagger.Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/php/")

	if err := gitPublish(ctx, t.Dagger.Git, gitPublishOpts{
		sdk:         "php",
		source:      gitRepoSource,
		sourcePath:  "sdk/php/",
		sourceTag:   tag,
		dest:        gitRepo,
		destTag:     version,
		username:    gitUserName,
		email:       gitUserEmail,
		githubToken: githubToken,
		dryRun:      dryRun,
	}); err != nil {
		return err
	}

	return nil
}

// Bump the PHP SDK's Engine dependency
func (t PHPSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	version = strings.TrimPrefix(version, "v")
	content := fmt.Sprintf(
		"<?php\n\n/* Code generated by dagger. DO NOT EDIT. */\nreturn '%s';\n",
		version,
	)

	dir := dag.Directory().WithNewFile(filepath.Join(phpSDKPath, phpSDKVersionFile), content)
	return dir, nil
}
