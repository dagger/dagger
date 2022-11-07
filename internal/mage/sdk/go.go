package sdk

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

const (
	goGeneratedAPIPath = "sdk/go/api.gen.go"
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

	_, err = c.Container().
		From("golangci/golangci-lint:v1.48").
		WithMountedDirectory("/app", util.RepositoryGoCodeOnly(c)).
		WithWorkdir("/app/sdk/go").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"golangci-lint", "run", "-v", "--timeout", "5m"},
		}).ExitCode(ctx)
	if err != nil {
		return err
	}

	return lintGeneratedCode(goGeneratedAPIPath, func() error {
		return t.Generate(ctx)
	})
}

// Test tests the Go SDK
func (t Go) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	_, err = util.GoBase(c).
		WithWorkdir("sdk/go").
		Exec(dagger.ContainerExecOpts{
			Args:                          []string{"go", "test", "-v", "./..."},
			ExperimentalPrivilegedNesting: true,
		}).
		ExitCode(ctx)
	return err
}

// Generate re-generates the SDK API
func (t Go) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	generated, err := util.GoBase(c).
		WithMountedFile("/usr/local/bin/cloak", util.DaggerBinary(c)).
		WithWorkdir("sdk/go").
		Exec(dagger.ContainerExecOpts{
			Args:                          []string{"go", "generate", "-v", "./..."},
			ExperimentalPrivilegedNesting: true,
		}).
		File(path.Base(goGeneratedAPIPath)).
		Contents(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(goGeneratedAPIPath, []byte(generated), 0600)
}

// Publish publishes the Go SDK
func (t Go) Publish(ctx context.Context, tag string) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	var (
		targetTag  = strings.TrimPrefix(tag, "sdk/go/")
		targetRepo = "https://github.com/aluzzardi/test.git"
		pat        = os.Getenv("GITHUB_PAT")
	)

	if pat == "" {
		return errors.New("export GITHUB_PAT=xxx before using this")
	}

	encodedPAT := base64.URLEncoding.EncodeToString([]byte("pat:" + pat))

	git := util.GoBase(c).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"apk", "add", "-U", "--no-cache", "git"},
		}).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"git", "config", "--global", "user.name", "dagger-ci"},
		}).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"git", "config", "--global", "user.email", "hello@dagger.io"},
		}).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"git", "config", "--global",
				"http.https://github.com/.extraheader",
				fmt.Sprintf("AUTHORIZATION: Basic %s", encodedPAT),
			},
		})

	repository := git.Exec(dagger.ContainerExecOpts{
		Args: []string{"git", "clone", "https://github.com/dagger/dagger.git", "/src/dagger"},
	}).WithWorkdir("/src/dagger")

	filtered := repository.
		WithEnvVariable("FILTER_BRANCH_SQUELCH_WARNING", "1").
		Exec(dagger.ContainerExecOpts{
			Args: []string{
				"git", "filter-branch", "-f", "--prune-empty",
				"--subdirectory-filter", "sdk/go",
				"--tree-filter", "if [ -f go.mod ]; then go mod edit -dropreplace github.com/dagger/dagger; fi",
				"--", tag,
			},
		})

	// Push
	_, err = filtered.Exec(dagger.ContainerExecOpts{
		Args: []string{
			"git",
			"push",
			"-f",
			targetRepo,
			fmt.Sprintf("%s:%s", tag, targetTag),
		},
	}).ExitCode(ctx)

	return err
}
