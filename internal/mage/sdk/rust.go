package sdk

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/internal/mage/util"
)

const (
	rustGeneratedAPIPath = "sdk/rust/crates/dagger-sdk/src/gen.rs"
	rustVersionFilePath  = "sdk/rust/crates/dagger-sdk/src/core/mod.rs"
	// https://hub.docker.com/_/rust
	rustDockerStable = "rust:1.71-bookworm"
)

var _ SDK = Rust{}

type Rust mg.Namespace

// Bump the Rust SDK's Engine dependency
func (Rust) Bump(ctx context.Context, engineVersion string) error {
	versionStr := `pub const DAGGER_ENGINE_VERSION: &'static str = "([0-9\.-a-zA-Z]+)";`
	versionStrf := `pub const DAGGER_ENGINE_VERSION: &'static str = "%s";`
	version := strings.TrimPrefix(engineVersion, "v")

	versionContents, err := os.ReadFile(rustVersionFilePath)
	if err != nil {
		return err
	}

	versionRe, err := regexp.Compile(versionStr)
	if err != nil {
		return err
	}

	versionBumpedContents := versionRe.ReplaceAll(
		versionContents,
		[]byte(fmt.Sprintf(versionStrf, version)),
	)

	err = os.WriteFile(rustVersionFilePath, versionBumpedContents, 0o600)
	if err != nil {
		return err
	}

	return nil
}

// Generate re-generates the SDK API
func (r Rust) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("rust").Pipeline("generate")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(
		ctx,
		c.Pipeline("dev-engine"),
		util.DevEngineOpts{Name: "sdk-rust-test"},
	)
	if err != nil {
		return err
	}

	cliBinPath := "/.dagger-cli"

	generated := r.rustBase(ctx, c.Pipeline(rustDockerStable), rustDockerStable).
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"cargo", "run", "-p", "dagger-bootstrap", "generate", "--output", fmt.Sprintf("/%s", rustGeneratedAPIPath)}).
		WithExec([]string{"cargo", "fix", "--all", "--allow-no-vcs"}).
		WithExec([]string{"cargo", "fmt"})

	contents, err := generated.File(strings.TrimPrefix(rustGeneratedAPIPath, "sdk/rust/")).
		Contents(ctx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(rustGeneratedAPIPath, []byte(contents), 0o600); err != nil {
		return err
	}

	return nil
}

// Lint lints the Rust SDK
func (r Rust) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("rust").Pipeline("lint")

	eg, gctx := errgroup.WithContext(ctx)

	base := r.rustBase(ctx, c, rustDockerStable)

	eg.Go(func() error {
		_, err = base.
			WithExec([]string{"cargo", "check", "--all", "--release"}).
			Sync(gctx)

		return err
	})

	eg.Go(func() error {
		_, err = base.
			WithExec([]string{"cargo", "fmt", "--check"}).
			Sync(gctx)

		return err
	})

	eg.Go(func() error {
		return util.LintGeneratedCode("sdk:rust:generate", func() error {
			return r.Generate(gctx)
		}, rustGeneratedAPIPath)
	})

	return eg.Wait()
}

// Publish publishes the Rust SDK
func (r Rust) Publish(ctx context.Context, tag string) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("rust").Pipeline("publish")

	dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN"))

	var (
		version = strings.TrimPrefix(tag, "sdk/rust/v")
		crate   = "dagger-sdk"
	)

	base := r.
		rustBase(ctx, c, rustDockerStable).
		WithExec([]string{
			"cargo", "install", "cargo-edit",
		}).
		WithExec([]string{
			"cargo", "set-version", "-p", crate, version,
		})

	args := []string{
		"cargo", "publish", "-p", crate, "-v", "--all-features",
	}

	if dryRun {
		args = append(args, "--dry-run")
		base = base.WithExec(args)
	} else {
		base = base.
			With(util.HostSecretVar(c, "CARGO_REGISTRY_TOKEN")).
			WithExec(args)
	}
	_, err = base.Sync(ctx)
	return err
}

// Test tests the Rust SDK
func (r Rust) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("rust").Pipeline("test")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(
		ctx,
		c.Pipeline("dev-engine"),
		util.DevEngineOpts{Name: "sdk-rust-test"},
	)
	if err != nil {
		return err
	}

	cliBinPath := "/.dagger-cli"

	_, err = r.rustBase(ctx, c.Pipeline(rustDockerStable), rustDockerStable).
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"rustc", "--version"}).
		WithExec([]string{"cargo", "test", "--release", "--all"}).
		Sync(ctx)
	return err
}

func (Rust) rustBase(ctx context.Context, c *dagger.Client, image string) *dagger.Container {
	const (
		appDir = "sdk/rust"
	)

	src := c.Directory().WithDirectory("/", util.Repository(c).Directory(appDir))

	mountPath := fmt.Sprintf("/%s", appDir)

	base := c.Container().
		From(image).
		WithDirectory(mountPath, src, dagger.ContainerWithDirectoryOpts{
			Include: []string{
				"**/Cargo.toml",
				"**/Cargo.lock",
				"**/main.rs",
				"**/lib.rs",
			},
		}).
		WithWorkdir(mountPath).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", c.CacheVolume("rust-cargo-"+image)).
		// combine into one layer so there's no assumptions on state of cache volume across steps
		With(util.ShellCmds(
			"rustup component add rustfmt",
			"cargo install cargo-chef",
			"cargo chef prepare --recipe-path /tmp/recipe.json",
			"cargo chef cook --release --workspace --recipe-path /tmp/recipe.json",
		)).
		WithMountedDirectory(mountPath, src)

	return base
}
