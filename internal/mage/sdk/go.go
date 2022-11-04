package sdk

import (
	"context"
	"fmt"
	"os"
	"path"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/google/go-cmp/cmp"
	"github.com/magefile/mage/mg"
)

const (
	generatedSDKPath = "sdk/go/api.gen.go"
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

	// Lint generated code
	// - Read currently generated code
	// - Generate again
	// - Compare
	// - Restore original generated code.
	original, err := os.ReadFile(generatedSDKPath)
	if err != nil {
		return err
	}
	defer os.WriteFile(generatedSDKPath, original, 0600)

	if err := t.Generate(ctx); err != nil {
		return err
	}
	new, err := os.ReadFile(generatedSDKPath)
	if err != nil {
		return err
	}

	diff := cmp.Diff(string(original), string(new))
	if diff != "" {
		return fmt.Errorf("generated api mismatch. please run `mage sdk:go:generate`:\n%s", diff)
	}

	return err
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
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "/usr/local/bin", "-ldflags", "-s -w", "./cmd/cloak"},
		}).
		WithWorkdir("sdk/go").
		Exec(dagger.ContainerExecOpts{
			Args:                          []string{"go", "generate", "-v", "./..."},
			ExperimentalPrivilegedNesting: true,
		}).
		File(path.Base(generatedSDKPath)).
		Contents(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(generatedSDKPath, []byte(generated), 0600)
}
