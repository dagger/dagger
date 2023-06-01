package sdk

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

const (
	elixirSDKPath          = "sdk/elixir"
	elixirSDKGeneratedPath = elixirSDKPath + "/lib/dagger/gen"
)

var _ SDK = Elixir{}

type Elixir mg.Namespace

// Lint lints the Elixir SDK
func (Elixir) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("elixir").Pipeline("lint")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(
		ctx,
		c.Pipeline("dev-engine"),
		util.DevEngineOpts{Name: "sdk-elixir-test"},
	)
	if err != nil {
		return err
	}

	cliBinPath := "/.dagger-cli"

	_, err = elixirBase(c, "1.14.5", "25.3", "20230227").
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"mix", "format", "--check-formatted"}).
		ExitCode(ctx)
	if err != nil {
		return err
	}

	return nil
}

// Test tests the Elixir SDK
func (Elixir) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("elixir").Pipeline("test")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(
		ctx,
		c.Pipeline("dev-engine"),
		util.DevEngineOpts{Name: "sdk-elixir-test"},
	)
	if err != nil {
		return err
	}

	cliBinPath := "/.dagger-cli"

	_, err = elixirBase(c, "1.14.5", "25.3", "20230227").
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"mix", "test"}).
		ExitCode(ctx)
	if err != nil {
		return err
	}
	return nil
}

// Generate re-generates the SDK API
func (Elixir) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("elixir").Pipeline("generate")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(
		ctx,
		c.Pipeline("dev-engine"),
		util.DevEngineOpts{Name: "sdk-elixir-test"},
	)
	if err != nil {
		return err
	}

	cliBinPath := "/.dagger-cli"

	generated := elixirBase(c, "1.14.5", "25.3", "20230227").
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"mix", "dagger.gen"})

	if err := os.RemoveAll(elixirSDKGeneratedPath); err != nil {
		return err
	}

	ok, err := generated.
		Directory(strings.Replace(elixirSDKGeneratedPath, elixirSDKPath +"/", "", 1)).
		Export(ctx, elixirSDKGeneratedPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("Cannot export generated code to `%s`", elixirSDKGeneratedPath)
	}
	return nil
}

// Publish publishes the Elixir SDK
func (Elixir) Publish(ctx context.Context, tag string) error {
	return nil
}

// Bump the Elixir SDK's Engine dependency
func (Elixir) Bump(ctx context.Context, engineVersion string) error {
	return nil
}

func elixirBase(c *dagger.Client, elixirVersion, otpVersion, debianVersion string) *dagger.Container {
	const appDir = "sdk/elixir"
	src := c.Directory().WithDirectory("/", util.Repository(c).Directory(appDir))

	mountPath := fmt.Sprintf("/%s", appDir)

	return c.Container().
		From(fmt.Sprintf("hexpm/elixir:%s-erlang-%s-debian-buster-%s-slim", elixirVersion, otpVersion, debianVersion)).
		WithWorkdir(mountPath).
		WithDirectory(mountPath, src).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"}).
		WithExec([]string{"mix", "deps.get"})
}
