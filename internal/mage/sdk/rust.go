package sdk

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/internal/mage/util"
)

const (
	rustGeneratedAPIPath = "sdk/rust/crates/dagger-sdk/src/gen.rs"
)

var _ SDK = Rust{}

type Rust mg.Namespace

// Bump implements SDK
func (Rust) Bump(ctx context.Context, engineVersion string) error {
	panic("unimplemented")
}

// Generate implements SDK
func (Rust) Generate(ctx context.Context) error {
	panic("unimplemented")
}

// Lint implements SDK
func (Rust) Lint(ctx context.Context) error {
	panic("unimplemented")
}

// Publish implements SDK
func (Rust) Publish(ctx context.Context, tag string) error {
	panic("unimplemented")
}

// Test implements SDK
func (r Rust) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(
		ctx,
		c.Pipeline("dev-engine"),
		util.DevEngineOpts{Name: "sdk-rust-test"},
	)
	if err != nil {
		return err
	}

	cliBinPath := "./dagger-cli"

	_, err = r.rustBase(ctx, c.Pipeline("nightly"), "nightly").
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"cargo", "test", "--release", "--all"}).
		ExitCode(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (Rust) rustBase(ctx context.Context, c *dagger.Client, tag string) *dagger.Container {
	const (
		appDir = "sdk/rust"
	)

	src := c.Directory().WithDirectory("/", util.Repository(c).Directory(appDir))

	mountPath := fmt.Sprintf("/%s", appDir)

	base := c.
		Container().
		From(fmt.Sprintf("rustlang/rust:%s", tag)).
		WithMountedCache("~/.cargo", c.CacheVolume("rust-cargo")).
		WithExec([]string{"cargo", "install", "cargo-chef"}).
		WithWorkdir(mountPath).
		WithDirectory(mountPath, src, dagger.ContainerWithDirectoryOpts{
			Include: []string{
				"**/Cargo.toml",
				"**/Cargo.lock",
				"**/main.rs",
				"**/lib.rs",
			},
		}).
		WithExec([]string{
			"mkdir", "-p", "/mnt/recipe",
		}).
		WithMountedCache("/mnt/recipe", c.CacheVolume("rust-chef-recipe")).
		WithExec([]string{
			"cargo", "chef", "prepare", "--recipe-path", "/mnt/recipe/recipe.json",
		}).
		WithMountedCache(fmt.Sprintf("%s/target", mountPath), c.CacheVolume("rust-target")).
		WithExec([]string{
			"cargo", "chef", "cook", "--release", "--workspace", "--recipe-path", "/mnt/recipe/recipe.json",
		}).
		WithMountedDirectory(mountPath, src)

	return base
}
