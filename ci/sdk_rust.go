package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/sync/errgroup"

	"dagger/util"
)

const (
	rustGeneratedAPIPath = "sdk/rust/crates/dagger-sdk/src/gen.rs"
	rustVersionFilePath  = "sdk/rust/crates/dagger-sdk/src/core/mod.rs"

	// https://hub.docker.com/_/rust
	rustDockerStable = "rust:1.71-bookworm"
	cargoChefVersion = "0.1.62"
)

type RustSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the Rust SDK
func (r RustSDK) Lint(ctx context.Context) error {
	base := r.rustBase(rustDockerStable)

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		_, err := base.
			WithExec([]string{"cargo", "check", "--all", "--release"}).
			Sync(ctx)
		return err
	})

	eg.Go(func() error {
		_, err := base.
			WithExec([]string{"cargo", "fmt", "--check"}).
			Sync(ctx)
		return err
	})

	eg.Go(func() error {
		return util.DiffDirectoryF(ctx, "sdk/rust", r.Dagger.Source, r.Generate)
	})

	return eg.Wait()
}

// Test tests the Rust SDK
func (r RustSDK) Test(ctx context.Context) error {
	ctr, err := r.Dagger.installDagger(ctx, r.rustBase(rustDockerStable), "sdk-rust-test")
	if err != nil {
		return err
	}
	_, err = ctr.
		WithExec([]string{"rustc", "--version"}).
		WithExec([]string{"cargo", "test", "--release", "--all"}).
		Sync(ctx)
	return err
}

// Generate re-generates the Rust SDK API
func (r RustSDK) Generate(ctx context.Context) (*Directory, error) {
	ctr, err := r.Dagger.installDagger(ctx, r.rustBase(rustDockerStable), "sdk-rust-generate")
	if err != nil {
		return nil, err
	}

	generated := ctr.
		WithExec([]string{"cargo", "run", "-p", "dagger-bootstrap", "generate", "--output", fmt.Sprintf("/%s", rustGeneratedAPIPath)}).
		WithExec([]string{"cargo", "fix", "--all", "--allow-no-vcs"}).
		WithExec([]string{"cargo", "fmt"}).
		File(strings.TrimPrefix(rustGeneratedAPIPath, "sdk/rust/"))

	return dag.Directory().
		WithDirectory("sdk/rust", r.Dagger.Source.Directory("sdk/rust")).
		WithFile(rustGeneratedAPIPath, generated), nil
}

// Publish publishes the Rust SDK
func (r RustSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,

	// +optional
	cargoRegistryToken *Secret,
) error {
	var (
		version = strings.TrimPrefix(tag, "sdk/rust/v")
		crate   = "dagger-sdk"
	)

	base := r.
		rustBase(rustDockerStable).
		WithExec([]string{
			"cargo", "install", "cargo-edit", "--locked",
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
			WithSecretVariable("CARGO_REGISTRY_TOKEN", cargoRegistryToken).
			WithExec(args)
	}

	_, err := base.Sync(ctx)
	return err
}

// Bump the Rust SDK's Engine dependency
func (r RustSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	versionStr := `pub const DAGGER_ENGINE_VERSION: &'static str = "([0-9\.-a-zA-Z]+)";`
	versionStrf := `pub const DAGGER_ENGINE_VERSION: &'static str = "%s";`
	version = strings.TrimPrefix(version, "v")

	versionContents, err := r.Dagger.Source.File(rustVersionFilePath).Contents(ctx)
	if err != nil {
		return nil, err
	}

	versionRe, err := regexp.Compile(versionStr)
	if err != nil {
		return nil, err
	}

	versionBumpedContents := versionRe.ReplaceAllString(
		versionContents,
		fmt.Sprintf(versionStrf, version),
	)

	return dag.Directory().WithNewFile(rustVersionFilePath, versionBumpedContents), nil
}

func (r RustSDK) rustBase(image string) *Container {
	const appDir = "sdk/rust"

	src := dag.Directory().WithDirectory("/", r.Dagger.Source.Directory(appDir))

	mountPath := fmt.Sprintf("/%s", appDir)

	base := dag.Container().
		From(image).
		WithDirectory(mountPath, src, ContainerWithDirectoryOpts{
			Include: []string{
				"**/Cargo.toml",
				"**/Cargo.lock",
				"**/main.rs",
				"**/lib.rs",
			},
		}).
		WithWorkdir(mountPath).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", dag.CacheVolume("rust-cargo-"+image)).
		// combine into one layer so there's no assumptions on state of cache volume across steps
		With(util.ShellCmds(
			"rustup component add rustfmt",
			"cargo install --locked cargo-chef@"+cargoChefVersion,
			"cargo chef prepare --recipe-path /tmp/recipe.json",
			"cargo chef cook --release --workspace --recipe-path /tmp/recipe.json",
		)).
		WithMountedDirectory(mountPath, src)

	return base
}
