package main

import (
	"context"
	"dagger/consts"
	"dagger/util"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/moby/buildkit/identity"
	"golang.org/x/sync/errgroup"
)

type GoSDK struct {
	Dagger *Dagger // +private
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
		return util.DiffDirectoryF(ctx, "sdk/go", util.GoDirectory(t.Dagger.Source), t.Generate)
	})
	return eg.Wait()
}

// Test tests the Go SDK
func (t GoSDK) Test(ctx context.Context) error {
	ctr, err := t.Dagger.installDagger(ctx, util.GoBase(t.Dagger.Source), "sdk-go-test")
	if err != nil {
		return err
	}

	output, err := ctr.
		WithWorkdir("sdk/go").
		WithExec([]string{"go", "test", "-v", "./..."}).
		Stdout(ctx)
	if err != nil {
		err = fmt.Errorf("test failed: %w\n%s", err, output)
	}
	return err
}

// Generate re-generates the Go SDK API
func (t GoSDK) Generate(ctx context.Context) (*Directory, error) {
	ctr, err := t.Dagger.installDagger(ctx, util.GoBase(t.Dagger.Source), "sdk-go-generate")
	if err != nil {
		return nil, err
	}

	generated := ctr.
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
	return gitPublish(ctx, gitPublishOpts{
		source:      "https://github.com/dagger/dagger.git",
		sourcePath:  "sdk/go/",
		sourceTag:   tag,
		dest:        gitRepo,
		destTag:     strings.TrimPrefix(tag, "sdk/go/"),
		username:    gitUserName,
		email:       gitUserEmail,
		githubToken: githubToken,
		dryRun:      dryRun,
	})
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

type gitPublishOpts struct {
	source, dest       string
	sourceTag, destTag string
	sourcePath         string

	username    string
	email       string
	githubToken *Secret

	dryRun bool
}

func gitPublish(ctx context.Context, opts gitPublishOpts) error {
	git := dag.Container().
		From(consts.AlpineImage).
		WithExec([]string{"apk", "add", "-U", "--no-cache", "git"}).
		WithExec([]string{"git", "config", "--global", "user.name", opts.username}).
		WithExec([]string{"git", "config", "--global", "user.email", opts.email})
	if !opts.dryRun {
		githubTokenRaw, err := opts.githubToken.Plaintext(ctx)
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
		WithExec([]string{"git", "clone", opts.source, "/src/dagger"}).
		WithWorkdir("/src/dagger").
		WithEnvVariable("FILTER_BRANCH_SQUELCH_WARNING", "1").
		WithExec([]string{
			"git", "filter-branch", "-f", "--prune-empty",
			"--subdirectory-filter", opts.sourcePath,
			"--tree-filter", "if [ -f go.mod ]; then go mod edit -dropreplace github.com/dagger/dagger; fi",
			"--", opts.sourceTag,
		})
	if !opts.dryRun {
		result = result.WithExec([]string{
			"git",
			"push",
			"-f",
			opts.dest,
			fmt.Sprintf("%s:%s", opts.sourceTag, opts.destTag),
		})
	}
	_, err := result.Sync(ctx)
	return err
}
