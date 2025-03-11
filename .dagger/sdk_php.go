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
	phpDoctumVersion    = "5.5.4"
	phpDoctumConfig     = `<?php

use Doctum\Doctum;
use Symfony\Component\Finder\Finder;

$iterator = Finder::create()
    ->files()
    ->name("*.php")
    ->exclude(".changes")
    ->exclude("docker")
    ->exclude("dev")
    ->exclude("runtime")
    ->exclude("tests")
    ->exclude("src/Codegen/")
    ->exclude("src/Command/")
    ->exclude("src/Connection/")
    ->exclude("src/Exception/")
    ->exclude("src/GraphQl/")
    ->exclude("src/Service/")
    ->exclude("src/ValueObject/")
    ->exclude("vendor")
    ->in("/src/sdk/php");

return new Doctum($iterator);`
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
		src := t.Dagger.Source().Directory(phpSDKPath)
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
		before := t.Dagger.Source()
		after, err := t.Generate(ctx)
		if err != nil {
			return err
		}
		return dag.Dirdiff().AssertEqual(ctx, before, after, []string{
			filepath.Join(phpSDKPath, phpSDKGeneratedDir),
		})
	})

	return eg.Wait()
}

// Test the PHP SDK
func (t PHPSDK) Test(ctx context.Context) error {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return err
	}

	src := t.Dagger.Source().Directory(phpSDKPath)
	base := t.phpBase().
		With(installer).
		WithEnvVariable("PATH", "./vendor/bin:$PATH", dagger.ContainerWithEnvVariableOpts{Expand: true})

	dev := dag.PhpSDKDev(dagger.PhpSDKDevOpts{Container: base, Source: src})
	_, err = dev.Test().Sync(ctx)
	return err
}

// Regenerate the PHP SDK API
func (t PHPSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return nil, err
	}

	generated := t.phpBase().
		With(installer).
		WithoutDirectory(phpSDKGeneratedDir).
		WithDirectory(phpSDKGeneratedDir, dag.Directory()).
		WithExec([]string{"sh", "-c", "$_EXPERIMENTAL_DAGGER_CLI_BIN run ./scripts/codegen.php"}).
		Directory(".")

	return dag.Directory().
		WithDirectory(phpSDKPath, generated).
		WithDirectory(generatedPhpReferencePath, t.GenerateSdkReference()), nil
}

// Generate the PHP SDK API reference documentation
func (t PHPSDK) GenerateSdkReference() *dagger.Directory {
	return t.phpBase().
		WithExec([]string{"apk", "add", "--no-cache", "curl"}).
		WithExec([]string{"curl", "-o", "/usr/bin/doctum", "-O", fmt.Sprintf("https://doctum.long-term.support/releases/%s/doctum.phar", phpDoctumVersion)}).
		WithExec([]string{"chmod", "+x", "/usr/bin/doctum"}).
		WithNewFile("/tmp/doctum-config.php", phpDoctumConfig).
		WithExec([]string{"doctum", "update", "/tmp/doctum-config.php", "-v"}).
		Directory("/src/sdk/php/build")
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

// phpBase returns a PHP container with the PHP SDK source files
// added and dependencies installed.
func (t PHPSDK) phpBase() *dagger.Container {
	src := t.Dagger.Source().Directory(phpSDKPath)
	return dag.Container().
		From(fmt.Sprintf("%s@%s", phpSDKImage, phpSDKDigest)).
		WithExec([]string{"apk", "add", "git"}).
		WithFile("/usr/bin/composer", dag.Container().From(phpSDKComposerImage).File("/usr/bin/composer")).
		WithMountedCache("/root/.composer", dag.CacheVolume(fmt.Sprintf("composer-%s", phpSDKImage))).
		WithEnvVariable("COMPOSER_HOME", "/root/.composer").
		WithEnvVariable("COMPOSER_NO_INTERACTION", "1").
		WithEnvVariable("COMPOSER_ALLOW_SUPERUSER", "1").
		WithWorkdir(fmt.Sprintf("/src/%s", phpSDKPath)).
		WithFile("composer.json", src.File("composer.json")).
		WithFile("composer.lock", src.File("composer.lock")).
		WithExec([]string{"composer", "install"}).
		WithDirectory(".", src)
}
