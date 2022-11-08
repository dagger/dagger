package sdk

import (
	"context"
	"os"
	"path"

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

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		_, err = util.GoBase(c).
			WithWorkdir("sdk/go").
			Exec(dagger.ContainerExecOpts{
				Args:                          []string{"go", "test", "-v", "./..."},
				ExperimentalPrivilegedNesting: true,
			}).
			ExitCode(ctx)
		return err
	})
}

// Generate re-generates the SDK API
func (t Go) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
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
	})
}
