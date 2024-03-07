package main

import (
	"context"
	"dagger/internal/dagger"
	"dagger/util"
	"fmt"
)

const (
	phpSDKPath         = "sdk/php"
	phpSDKGeneratedDir = "generated"
	phpSDKVersionFile  = "src/Connection/version.php"
)

type PHPSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the PHP SDK
func (t PHPSDK) Lint(ctx context.Context) error {
	return util.DiffDirectoryF(ctx, "sdk/php", t.Dagger.Source, t.Generate)
}

// Test tests the PHP SDK
func (t PHPSDK) Test(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

// Generate re-generates the PHP SDK API
func (t PHPSDK) Generate(ctx context.Context) (*Directory, error) {
	ctr, err := t.Dagger.installDagger(ctx, t.phpBase(), "sdk-php-generate")
	if err != nil {
		return nil, err
	}

	generated := ctr.
		With(util.ShellCmds(
			fmt.Sprintf("rm -f %s/*.php", phpSDKGeneratedDir),
			fmt.Sprintf("ls -lha"),
			"$_EXPERIMENTAL_DAGGER_CLI_BIN run ./codegen",
		)).
		Directory(".")
	return dag.Directory().WithDirectory(phpSDKPath, generated), nil
}

// Publish publishes the PHP SDK
func (t PHPSDK) Publish(ctx context.Context, tag string) error {
	// TODO: port php publish
	return fmt.Errorf("not implemented")
}

// Bump the PHP SDK's Engine dependency
func (t PHPSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	// TODO: port php publish
	return nil, fmt.Errorf("not implemented")
}

// phpBase returns a PHP container with the PHP SDK source files
// added and dependencies installed.
func (t PHPSDK) phpBase() *dagger.Container {
	src := t.Dagger.Source.Directory(phpSDKPath)
	return dag.Container().
		From("php:8.2-zts-bookworm").
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "-y", "git", "unzip"}).
		WithFile("/usr/bin/composer", dag.Container().
			From("composer:2").
			File("/usr/bin/composer"),
		).
		WithMountedCache("/root/.composer", dag.CacheVolume("composer-cache-8.2-zts-bookworm")).
		WithEnvVariable("COMPOSER_HOME", "/root/.composer").
		WithEnvVariable("COMPOSER_ALLOW_SUPERUSER", "1").
		WithWorkdir(fmt.Sprintf("/%s", phpSDKPath)).
		WithFile("composer.json", src.File("composer.json")).
		WithFile("composer.lock", src.File("composer.lock")).
		WithExec([]string{"composer", "install"}).
		WithDirectory(".", src)
}
