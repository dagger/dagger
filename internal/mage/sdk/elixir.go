package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

const (
	elixirSDKPath            = "sdk/elixir"
	elixirSDKGeneratedPath   = elixirSDKPath + "/lib/dagger/gen"
	elixirSDKVersionFilePath = elixirSDKPath + "/lib/dagger/engine_conn.ex"

	// https://hub.docker.com/r/hexpm/elixir/tags?page=1&name=debian-buster
	elixirVersion = "1.14.5"
	otpVersion    = "25.3"
	debianVersion = "20230227"
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

	_, err = elixirBase(c).
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"mix", "lint"}).
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

	_, err = elixirBase(c).
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

	generated := elixirBase(c).
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"mix", "dagger.gen"})

	if err := os.RemoveAll(elixirSDKGeneratedPath); err != nil {
		return err
	}

	ok, err := generated.
		Directory(strings.Replace(elixirSDKGeneratedPath, elixirSDKPath+"/", "", 1)).
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
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	var (
		version     = strings.TrimPrefix(tag, "sdk/elixir/v")
		versionFile = "sdk/elixir/VERSION"
		hexAPIKey   = os.Getenv("HEX_API_KEY")
		dryRun      = os.Getenv("HEX_DRY_RUN")
	)

	if hexAPIKey == "" {
		return errors.New("HEX_API_KEY environment variable must be set")
	}

	if err := os.WriteFile(versionFile, []byte(version), 0o600); err != nil {
		return err
	}
	defer func() {
		// Ensure to not make version file dirty.
		os.WriteFile(versionFile, []byte("0.0.0\n"), 0o600)
	}()

	args := []string{"mix", "hex.publish", "--yes"}
	if dryRun != "" {
		args = append(args, "--dry-run")
	}

	// TODO: copy LICENSE?

	c = c.Pipeline("sdk").Pipeline("elixir").Pipeline("generate")

	_, err = elixirBase(c).
		WithEnvVariable("HEX_API_KEY", hexAPIKey).
		WithExec(args).
		Sync(ctx)
	return err
}

// Bump the Elixir SDK's Engine dependency
func (Elixir) Bump(ctx context.Context, engineVersion string) error {
	contents, err := os.ReadFile(elixirSDKVersionFilePath)
	if err != nil {
		return err
	}

	newVersion := fmt.Sprintf(`@dagger_cli_version "%s"`, strings.TrimPrefix(engineVersion, "v"))

	versionRe, err := regexp.Compile(`@dagger_cli_version "([0-9\.-a-zA-Z]+)"`)
	if err != nil {
		return err
	}
	newContents := versionRe.ReplaceAll(contents, []byte(newVersion))
	return os.WriteFile(elixirSDKVersionFilePath, newContents, 0o600)
}

func elixirBase(c *dagger.Client) *dagger.Container {
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
