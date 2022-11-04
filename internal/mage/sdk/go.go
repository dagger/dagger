package sdk

import (
	"context"
	"fmt"
	"os"
	"path"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/generator"
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

	repo := c.Host().Workdir()

	golangci := c.Container().From("golangci/golangci-lint:v1.45")

	_, err = golangci.
		WithMountedDirectory("/app", repo).
		WithWorkdir("/app/sdk/go").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"golangci-lint", "run", "-v", "--timeout", "1m"},
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

	// FIXME: for now, generate using the internal code directly
	// Running the Generate target requires running dagger in dagger.
	// if err := t.Generate(ctx); err != nil {
	// 	return err
	// }
	// new, err := os.ReadFile(generatedSDKPath)
	// if err != nil {
	// 	return err
	// }

	new, err := generator.IntrospectAndGenerate(ctx, c, generator.Config{
		Package: "dagger",
	})
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

	repo := c.Host().Workdir()

	_, err = goContainer(c, repo).
		WithWorkdir("sdk/go").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "test", "-v", "./..."},
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

	repo := c.Host().Workdir()

	generated, err := goContainer(c, repo).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "build", "-o", "/usr/local/bin", "-ldflags", "-s -w", "./cmd/cloak"},
		}).
		WithWorkdir("sdk/go").
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "generate", "-v", "./..."},
		}).
		File(path.Base(generatedSDKPath)).
		Contents(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(generatedSDKPath, []byte(generated), 0600)
}

func goContainer(c *dagger.Client, repo *dagger.Directory) *dagger.Container {
	// Create a directory containing only `go.{mod,sum}` files.
	goMods := c.Directory()
	for _, f := range []string{"go.mod", "go.sum", "sdk/go/go.mod", "sdk/go/go.sum"} {
		goMods = goMods.WithFile(f, repo.File(f))
	}

	return c.Container().
		From("golang:1.19-alpine").
		WithEnvVariable("CGO_ENABLED", "0").
		// WithEnvVariable("GOOS", runtime.GOOS).
		// WithEnvVariable("GOARCH", runtime.GOARCH).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithMountedDirectory("/app", goMods).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"go", "mod", "download"},
		}).
		// run `go build` with all source
		WithMountedDirectory("/app", repo)
}
