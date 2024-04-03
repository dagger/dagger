package sdk

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/moby/buildkit/identity"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
)

const (
	goGeneratedAPIPath = "sdk/go/"
)

var _ SDK = Go{}

type Go mg.Namespace

// Lint lints the Go SDK
func (t Go) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("go").Pipeline("lint")

	_, err = c.Container().
		From("golangci/golangci-lint:v1.54-alpine").
		WithMountedDirectory("/app", util.RepositoryGoCodeOnly(c)).
		WithWorkdir("/app/sdk/go").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Sync(ctx)
	if err != nil {
		return err
	}

	return util.LintGeneratedCode("sdk:go:generate", func() error {
		return t.Generate(ctx)
	}, goGeneratedAPIPath)
}

// Test tests the Go SDK
func (t Go) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("go").Pipeline("test")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(ctx, c.Pipeline("dev-engine"), util.DevEngineOpts{Name: "sdk-go-test"})
	if err != nil {
		return err
	}

	cliBinary, err := util.DevelDaggerBinary(ctx, c)
	if err != nil {
		return err
	}
	cliBinPath := "/.dagger-cli"

	output, err := util.GoBase(c).
		WithWorkdir("sdk/go").
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"go", "test", "-v", "./..."}).
		Stdout(ctx)
	if err != nil {
		err = fmt.Errorf("test failed: %w\n%s", err, output)
	}
	return err
}

// Generate re-generates the SDK API
func (t Go) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("go").Pipeline("generate")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(ctx, c.Pipeline("dev-engine"), util.DevEngineOpts{Name: "sdk-go-generate"})
	if err != nil {
		return err
	}

	cliBinary, err := util.DevelDaggerBinary(ctx, c)
	if err != nil {
		return err
	}
	cliBinPath := "/.dagger-cli"

	generated := util.GoBase(c).
		WithServiceBinding("dagger-engine", devEngine).
		WithMountedFile("/usr/local/bin/dagger", cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithWorkdir("sdk/go").
		WithExec([]string{"go", "generate", "-v", "./..."}).
		WithExec([]string{"go", "mod", "tidy"}).
		Directory(".")
	_, err = generated.Export(ctx, goGeneratedAPIPath)
	if err != nil {
		return err
	}
	return nil
}

// Publish publishes the Go SDK
func (t Go) Publish(ctx context.Context, tag string) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("go").Pipeline("publish")

	var targetTag = strings.TrimPrefix(tag, "sdk/go/")

	dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN"))

	var targetRepo = os.Getenv("TARGET_REPO")
	if targetRepo == "" {
		targetRepo = "https://github.com/dagger/dagger-go-sdk.git"
	}

	var gitUserName = os.Getenv("GIT_USER_NAME")
	if gitUserName == "" {
		gitUserName = "dagger-ci"
	}

	var gitUserEmail = os.Getenv("GIT_USER_EMAIL")
	if gitUserEmail == "" {
		gitUserEmail = "hello@dagger.io"
	}

	git := util.GoBase(c).
		WithExec([]string{"apk", "add", "-U", "--no-cache", "git"}).
		WithExec([]string{"git", "config", "--global", "user.name", gitUserName}).
		WithExec([]string{"git", "config", "--global", "user.email", gitUserEmail})
	if !dryRun {
		pat := util.GetHostEnv("GITHUB_PAT")
		encodedPAT := base64.URLEncoding.EncodeToString([]byte("pat:" + pat))
		git = git.
			WithEnvVariable("GIT_CONFIG_COUNT", "1").
			WithEnvVariable("GIT_CONFIG_KEY_0", "http.https://github.com/.extraheader").
			WithSecretVariable("GIT_CONFIG_VALUE_0", c.SetSecret("GITHUB_HEADER", fmt.Sprintf("AUTHORIZATION: Basic %s", encodedPAT)))
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
			targetRepo,
			fmt.Sprintf("%s:%s", tag, targetTag),
		})
	}
	_, err = result.Sync(ctx)
	return err
}

// Bump the Go SDK's Engine dependency
func (t Go) Bump(ctx context.Context, version string) error {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")

	versionFile := fmt.Sprintf(`// Code generated by dagger. DO NOT EDIT.

package engineconn

const CLIVersion = %q
`, version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	return os.WriteFile("sdk/go/internal/engineconn/version.gen.go", []byte(versionFile), 0o600)
}
