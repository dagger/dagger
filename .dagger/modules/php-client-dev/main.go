// Toolchain to develop the Dagger PHP SDK (experimental)
package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/php-client-dev/internal/dagger"
)

const (
	phpSDKImage         = "php:8.3-cli-alpine"
	phpSDKDigest        = "sha256:e4ffe0a17a6814009b5f0713a5444634a9c5b688ee34b8399e7d4f2db312c3b4"
	phpSDKComposerImage = "composer/composer:2.8-bin" +
		"@sha256:c735b6a52ea118693178babc601984dbbbd07f1d31ec87eaa881173622b467ed"
)

type PhpClientDev struct {
	OriginalWorkspace  *dagger.Directory // +private
	Workspace          *dagger.Directory // +private
	SourcePath         string            // +private
	ClientDockerConfig *dagger.Secret    // +private
	Ws                 *dagger.Workspace // +private
}

// Develop the Dagger PHP SDK (experimental)
func New(
	// A directory with all the files needed to develop the SDK
	// +defaultPath="/"
	// +ignore=["*", "!sdk/php", "sdk/php/.changes"]
	workspaceDir *dagger.Directory,
	// The path of the SDK source in the workspace
	// +default="sdk/php"
	sourcePath string,
	// A docker config file with credentials to install on clients.
	// +optional
	clientDockerConfig *dagger.Secret,
	// Workspace forwarded to engine-dev for VCS stamping. Auto-injected on a
	// direct call; dependencies don't inherit it, so callers must forward it.
	ws *dagger.Workspace,
) *PhpClientDev {
	return &PhpClientDev{
		Workspace:          workspaceDir,
		OriginalWorkspace:  workspaceDir,
		SourcePath:         sourcePath,
		ClientDockerConfig: clientDockerConfig,
		Ws:                 ws,
	}
}

func (t PhpClientDev) BaseContainer() *dagger.Container {
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
			return dag.DaggerEngine(t.Ws, dagger.DaggerEngineOpts{
				ClientDockerConfig: t.ClientDockerConfig,
			}).InstallClient(c)
		})
}

// Returns the PHP SDK workspace mounted in a dev container,
// and working directory set to the SDK source
func (t PhpClientDev) DevContainer(
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
func (t PhpClientDev) Source() *dagger.Directory {
	return t.Workspace.Directory(t.SourcePath)
}

// Lint the PHP code with PHP CodeSniffer (https://github.com/squizlabs/PHP_CodeSniffer)
// +check
func (t PhpClientDev) PhpCodeSniffer(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"phpcs"}).
		Sync(ctx)

	return err
}

// Analyze the PHP code with PHPStan (https://phpstan.org)
// +check
func (t PhpClientDev) PhpStan(ctx context.Context) error {
	_, err := t.
		DevContainer(true).
		WithExec([]string{"phpstan", "--no-progress", "--memory-limit=1G"}).
		Sync(ctx)

	return err
}

// Test the PHP SDK with PHPUnit (https://phpunit.de/)
// +check
func (t PhpClientDev) Test(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"phpunit"}).Sync(ctx)

	return err
}

// Regenerate the PHP SDK API
// +generate
func (t *PhpClientDev) API() *dagger.Changeset {
	return t.WithGeneratedClient().Changes()
}

func (t *PhpClientDev) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t *PhpClientDev) WithGeneratedClient() *PhpClientDev {
	relLayer := t.DevContainer(true).
		WithExec([]string{"dagger", "api", "exec", "--load-workspace-modules", "./scripts/codegen.php"}).
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

// Test the publishing process
func (t PhpClientDev) ReleaseDryRun(
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
func (t PhpClientDev) VersionFromTag(tag string) string {
	prefix := strings.TrimRight(t.SourcePath, "/") + "/"
	return strings.TrimPrefix(tag, prefix)
}

// Publish the PHP SDK
func (t PhpClientDev) Release(
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
