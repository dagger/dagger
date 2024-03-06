package main

import (
	"context"
	"dagger/util"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/moby/buildkit/identity"
	"golang.org/x/sync/errgroup"
)

type GoSDK struct {
	Dagger *Dagger
}

// Lint lints the Go SDK
func (t GoSDK) Lint(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		_, err := dag.Container().
			From("golangci/golangci-lint:v1.54-alpine").
			WithMountedDirectory("/app", util.GoDirectory(t.Dagger.Source)).
			WithWorkdir("/app/sdk/go").
			WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
			Sync(ctx)
		return err
	})
	eg.Go(func() error {
		return diffDirectoryF(ctx, "sdk/go", util.GoDirectory(t.Dagger.Source), t.Generate)
	})
	return eg.Wait()
}

// Test tests the Go SDK
func (t GoSDK) Test(ctx context.Context) error {
	engineSvc, err := t.Dagger.Engine().Service(ctx, "sdk-go-test")
	if err != nil {
		return err
	}
	engineEndpoint, err := engineSvc.Endpoint(ctx, ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return err
	}

	cliBinary, err := t.Dagger.CLI().File(ctx)
	if err != nil {
		return err
	}
	cliBinaryPath := "/.dagger-cli"

	output, err := util.GoBase(t.Dagger.Source).
		WithWorkdir("sdk/go").
		WithServiceBinding("dagger-engine", engineSvc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineEndpoint).
		WithMountedFile(cliBinaryPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath).
		WithExec([]string{"go", "test", "-v", "./..."}).
		Stdout(ctx)
	if err != nil {
		err = fmt.Errorf("test failed: %w\n%s", err, output)
	}
	return err
}

// Generate re-generates the SDK API
func (t GoSDK) Generate(ctx context.Context) (*Directory, error) {
	engineSvc, err := t.Dagger.Engine().Service(ctx, "sdk-go-generate")
	if err != nil {
		return nil, err
	}
	engineEndpoint, err := engineSvc.Endpoint(ctx, ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinary, err := t.Dagger.CLI().File(ctx)
	if err != nil {
		return nil, err
	}
	cliBinaryPath := "/.dagger-cli"

	generated := util.GoBase(t.Dagger.Source).
		WithServiceBinding("dagger-engine", engineSvc).
		WithMountedFile("/usr/local/bin/dagger", cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineEndpoint).
		WithMountedFile(cliBinaryPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath).
		WithWorkdir("sdk/go").
		WithExec([]string{"go", "generate", "-v", "./..."}).
		WithExec([]string{"go", "mod", "tidy"}).
		Directory(".")
	return dag.Directory().WithDirectory("sdk/go", generated), nil
}

// Publish publishes the Go SDK
func (t GoSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,

	// +optional
	// +default="https://github.com/dagger/dagger-go-sdk.git"
	gitRepo string,
	// +optional
	// +default="dagger-ci"
	gitUserName string,
	// +optional
	// +default="hello@dagger.io"
	gitUserEmail string,

	// +optional
	githubToken *Secret,
) error {
	var targetTag = strings.TrimPrefix(tag, "sdk/go/")

	git := util.GoBase(t.Dagger.Source).
		WithExec([]string{"apk", "add", "-U", "--no-cache", "git"}).
		WithExec([]string{"git", "config", "--global", "user.name", gitUserName}).
		WithExec([]string{"git", "config", "--global", "user.email", gitUserEmail})
	if !dryRun {
		githubTokenRaw, err := githubToken.Plaintext(ctx)
		if err != nil {
			return err
		}
		encodedPAT := base64.URLEncoding.EncodeToString([]byte("pat:" + githubTokenRaw))
		git = git.
			WithEnvVariable("GIT_CONFIG_COUNT", "1").
			WithEnvVariable("GIT_CONFIG_KEY_0", "http.https://github.com/.extraheader").
			WithSecretVariable("GIT_CONFIG_VALUE_0", dag.SetSecret("GITHUB_HEADER", fmt.Sprintf("AUTHORIZATION: Basic %s", encodedPAT)))
	}

	result := git.
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"git", "clone", "https://github.com/dagger/dagger.git", "/src/dagger"}).
		WithWorkdir("/src/dagger").
		WithEnvVariable("FILTER_BRANCH_SQUELCH_WARNING", "1").
		WithExec([]string{
			"git", "filter-branch", "-f", "--prune-empty",
			"--subdirectory-filter", "sdk/go",
			"--tree-filter", "if [ -f go.mod ]; then go mod edit -dropreplace github.com/dagger/dagger; fi",
			"--", tag,
		})
	if !dryRun {
		result = result.WithExec([]string{
			"git",
			"push",
			"-f",
			gitRepo,
			fmt.Sprintf("%s:%s", tag, targetTag),
		})
	}
	_, err := result.Sync(ctx)
	return err
}

// Bump the Go SDK's Engine dependency
func (t GoSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	versionFile := fmt.Sprintf(`// Code generated by dagger. DO NOT EDIT.

package engineconn

const CLIVersion = %q
`, version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	dir := dag.Directory().WithNewFile("sdk/go/internal/engineconn/version.gen.go", versionFile)
	return dir, nil
}
