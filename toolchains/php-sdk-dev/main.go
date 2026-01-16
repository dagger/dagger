// Toolchain to develop the Dagger PHP SDK (experimental)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"dagger/php-sdk-dev/internal/dagger"
)

const (
	phpSDKImage         = "php:8.3-cli-alpine"
	phpSDKDigest        = "sha256:e4ffe0a17a6814009b5f0713a5444634a9c5b688ee34b8399e7d4f2db312c3b4"
	phpSDKComposerImage = "composer/composer:2.8-bin" +
		"@sha256:c735b6a52ea118693178babc601984dbbbd07f1d31ec87eaa881173622b467ed"
	phpSDKVersionFile = "src/Connection/version.php"

	phpDoctumVersion = "5.5.4"
)

type PhpSdkDev struct {
	OriginalWorkspace *dagger.Directory // +private
	Workspace         *dagger.Directory // +private
	DoctumConfigPath  string            // +private
	SourcePath        string            // +private
}

// Develop the Dagger PHP SDK (experimental)
func New(
	// A directory with all the files needed to develop the SDK
	// +defaultPath="/"
	// +ignore=["*", "!sdk/php", "!docs/doctum-config.php", "!docs/static/reference/php", "sdk/php/.changes"]
	workspace *dagger.Directory,
	// The path of the SDK source in the workspace
	// +default="sdk/php"
	sourcePath string,
	// The path of the doctum config in the workspace
	// +default="docs/doctum-config.php"
	doctumConfigPath string,
) *PhpSdkDev {
	return &PhpSdkDev{
		Workspace:         workspace,
		OriginalWorkspace: workspace,
		SourcePath:        sourcePath,
		DoctumConfigPath:  doctumConfigPath,
	}
}

func (t PhpSdkDev) BaseContainer() *dagger.Container {
	// Extract the PHP base container from the native SDK dev module
	// - We build the base container eagerly, to avoid keeping a reference to DaggerDev
	// - But we build the full dev container *lazily*, because we may have mutated our workspace with generated files
	composerBinary := dag.Container().
		From(phpSDKComposerImage).
		File("/composer")
	return dag.Container().
		From(phpSDKImage+"@"+phpSDKDigest).
		WithMountedFile("/usr/bin/composer", composerBinary).
		WithMountedCache(
			"/root/.composer",
			dag.CacheVolume(fmt.Sprintf("composer-%s", phpSDKImage)),
		).
		WithEnvVariable("COMPOSER_HOME", "/root/.composer").
		WithEnvVariable("COMPOSER_NO_INTERACTION", "1").
		WithEnvVariable("COMPOSER_ALLOW_SUPERUSER", "1").
		WithWorkdir("/src").
		With(func(c *dagger.Container) *dagger.Container {
			return dag.DaggerEngine().InstallClient(c)
		})
}

// Returns the PHP SDK workspace mounted in a dev container,
// and working directory set to the SDK source
func (t PhpSdkDev) DevContainer(
	// Run composer install before returning the container
	//+default="false"
	runInstall bool,
) *dagger.Container {
	ctr := t.BaseContainer().
		WithMountedDirectory(".", t.Workspace).
		WithWorkdir(t.SourcePath)

	if runInstall {
		ctr = ctr.
			WithExec([]string{"composer", "install"}).
			WithEnvVariable("PATH", "./vendor/bin:$PATH", dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			})
	}

	return ctr
}

// Source returns the source directory for the PHP SDK
func (t PhpSdkDev) Source() *dagger.Directory {
	return t.Workspace.Directory(t.SourcePath)
}

// DoctumConfig returns the doctum configuration file
func (t PhpSdkDev) DoctumConfig() *dagger.File {
	return t.Workspace.File(t.DoctumConfigPath)
}

// Lint the PHP code with PHP CodeSniffer (https://github.com/squizlabs/PHP_CodeSniffer)
// +check
func (t PhpSdkDev) PhpCodeSniffer(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"phpcs"}).
		Sync(ctx)

	return err
}

// Analyze the PHP code with PHPStan (https://phpstan.org)
// +check
func (t PhpSdkDev) PhpStan(ctx context.Context) error {
	_, err := t.
		DevContainer(true).
		WithExec([]string{"phpstan", "--no-progress", "--memory-limit=1G"}).
		Sync(ctx)

	return err
}

// Test the PHP SDK with PHPUnit (https://phpunit.de/)
// +check
func (t PhpSdkDev) Test(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"phpunit"}).Sync(ctx)

	return err
}

// Regenerate the PHP SDK API + docs
func (t *PhpSdkDev) Generate(ctx context.Context) (*dagger.Changeset, error) {
	t, err := t.
		WithGeneratedClient().
		WithGeneratedDocs(ctx)
	if err != nil {
		return nil, err
	}

	return t.Changes(), nil
}

func (t *PhpSdkDev) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t *PhpSdkDev) WithGeneratedClient() *PhpSdkDev {
	relLayer := t.DevContainer(true).
		WithExec([]string{"dagger", "run", "./scripts/codegen.php"}).
		Directory(".").
		Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{
				"vendor",
			},
		})

	t.Workspace = t.Workspace.
		// Merge rel layer inside the current workspace
		WithDirectory(t.SourcePath, relLayer)

	return t
}

// Generate reference docs from the generated client
// NOTE: it's the caller's responsibility to ensure the generated client is up-to-date
// (see WithGeneratedClient)
func (t *PhpSdkDev) WithGeneratedDocs(ctx context.Context) (*PhpSdkDev, error) {
	relLayer := t.DevContainer(false).
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
		// Merge relative layer with the current workspace
		WithDirectory("docs/static/reference/php", relLayer)

	return t, nil
}

// Test the publishing process
func (t PhpSdkDev) ReleaseDryRun(
	ctx context.Context,
	// Source git repository to fake-release
	// +defaultPath="/"
	sourceRepo *dagger.GitRepository,
	// Source git tag to fake-release
	// +default="HEAD"
	sourceTag string,
	// Target git remote to fake-release *to*
	// +default="https://github.com/dagger/dagger-php-sdk.git"
	destRemote string,
) error {
	return dag.GitReleaser().DryRun(
		ctx,
		sourceRepo,
		sourceTag,
		destRemote,
		dagger.GitReleaserDryRunOpts{
			DestTag:    t.VersionFromTag(sourceTag),
			SourcePath: "sdk/php/",
		},
	)
}

// Get v1.2.3 from sdk/php/v1.2.3
func (t PhpSdkDev) VersionFromTag(tag string) string {
	prefix := strings.TrimRight(t.SourcePath, "/") + "/"
	return strings.TrimPrefix(tag, prefix)
}

// Publish the PHP SDK
func (t PhpSdkDev) Release(
	ctx context.Context,

	// The source git repository to release
	// +defaultPath="/"
	sourceRepo *dagger.GitRepository,

	// The source git tag to release
	sourceTag string,

	// +optional
	// +default="https://github.com/dagger/dagger-php-sdk.git"
	dest string,
	// +optional
	githubToken *dagger.Secret,
) error {
	return dag.GitReleaser().Release(
		ctx,
		sourceRepo,
		sourceTag,
		dest,
		dagger.GitReleaserReleaseOpts{
			DestTag:     t.VersionFromTag(sourceTag),
			SourcePath:  "sdk/php/",
			GithubToken: githubToken,
		},
	)
}

// Bump the PHP SDK's Engine dependency
func (t PhpSdkDev) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	version = strings.TrimPrefix(version, "v")
	content := fmt.Sprintf(
		"<?php\n\n/* Code generated by dagger. DO NOT EDIT. */\nreturn '%s';\n",
		version,
	)

	layer := dag.Directory().WithNewFile(filepath.Join("sdk/php", phpSDKVersionFile), content)
	return layer.Changes(dag.Directory()).Sync(ctx)
}

func formatJSONFile(ctx context.Context, f *dagger.File) (*dagger.File, error) {
	name, err := f.Name(ctx)
	if err != nil {
		return nil, err
	}

	contents, err := f.Contents(ctx)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	err = json.Indent(&out, []byte(contents), "", "\t")
	if err != nil {
		return nil, err
	}

	return dag.File(name, out.String()), nil
}
