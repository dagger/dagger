package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/BurntSushi/toml"
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

const (
	rustVersionFilePath   = "sdk/rust/crates/dagger-sdk/src/core/version.rs"
	rustCargoTomlFilePath = "sdk/rust/Cargo.toml"
	rustCargoLockFilePath = "sdk/rust/Cargo.lock"

	cargoEditVersion = "0.13.0"
)

type RustSDK struct {
	Dagger *DaggerDev // +private
}

func (r RustSDK) Name() string {
	return "rust"
}

func (r RustSDK) CheckFormat(ctx context.Context) (MyCheckStatus, error) {
	_, err := r.DevContainer().
		WithExec([]string{"cargo", "fmt", "--check"}).
		Sync(ctx)
	return CheckCompleted, err
}

func (r RustSDK) CheckCompilation(ctx context.Context) (MyCheckStatus, error) {
	_, err := r.DevContainer().
		WithExec([]string{"cargo", "check", "--all", "--release"}).
		Sync(ctx)
	return CheckCompleted, err
}

// Test the Rust SDK
func (r RustSDK) Test(ctx context.Context) (MyCheckStatus, error) {
	_, err := r.DevContainer().
		With(r.Dagger.devEngineSidecar()).
		WithExec([]string{"rustc", "--version"}).
		WithExec([]string{"cargo", "test", "--release", "--all"}).
		Sync(ctx)
	return CheckCompleted, err
}

func (r RustSDK) Source() *dagger.Directory {
	return r.Dagger.Source.Filter(dagger.DirectoryFilterOpts{
		Include: []string{
			"sdk/rust/",
		},
	})
}

// Regenerate the Rust SDK API
func (r RustSDK) Generate(_ context.Context) (*dagger.Changeset, error) {
	genClientPath := "crates/dagger-sdk/src/gen.rs"
	relLayer := r.DevContainer().
		WithMountedFile("/introspection.json", r.Dagger.introspectionJSON()).
		WithExec([]string{"cargo", "run", "-p", "dagger-bootstrap", "generate", "/introspection.json", "--output", genClientPath}).
		WithExec([]string{"cargo", "fix", "--all", "--allow-no-vcs"}).
		WithExec([]string{"cargo", "fmt"}).
		Directory(".")
	absLayer := r.Source().
		WithoutDirectory("sdk/rust").
		WithDirectory("sdk/rust", relLayer)
	return changes(r.Source(), absLayer, []string{"sdk/rust/target"}), nil
}

// Test the publishing process
func (r RustSDK) ReleaseDryRun(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, r.Publish(ctx, "HEAD", true, nil)
}

// Publish the Rust SDK
func (r RustSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,

	// +optional
	cargoRegistryToken *dagger.Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/rust/")

	versionFlag := strings.TrimPrefix(version, "v")
	if !semver.IsValid(version) {
		// just pick any version, it's a dry-run
		versionFlag = "--bump=rc"
	}

	crate := "dagger-sdk"
	base := r.
		DevContainer().
		WithExec([]string{
			"cargo", "install", "cargo-edit@" + cargoEditVersion, "--locked",
		}).
		WithExec([]string{
			"cargo", "set-version", "-p", crate, versionFlag,
		})
	args := []string{
		"cargo", "publish", "-p", crate, "-v", "--all-features",
	}

	if dryRun {
		args = append(args, "--dry-run")
		base = base.WithExec(args)

		targetVersion := strings.TrimPrefix(version, "v")
		if !semver.IsValid(version) {
			cargoToml, err := base.File("Cargo.toml").Contents(ctx)
			if err != nil {
				return err
			}
			var config struct {
				Workspace struct {
					Package struct {
						Version string
					}
				}
			}
			_, err = toml.Decode(cargoToml, &config)
			if err != nil {
				return err
			}
			targetVersion = config.Workspace.Package.Version
		}

		// check we created the right files
		_, err := base.Directory(fmt.Sprintf("./target/package/dagger-sdk-%s", targetVersion)).Sync(ctx)
		if err != nil {
			return err
		}
		_, err = base.File(fmt.Sprintf("./target/package/dagger-sdk-%s.crate", targetVersion)).Sync(ctx)
		if err != nil {
			return err
		}

		// check that Cargo.toml got the version
		dt, err := base.File(fmt.Sprintf("./target/package/dagger-sdk-%s/Cargo.toml", targetVersion)).Contents(ctx)
		if err != nil {
			return err
		}
		if !strings.Contains(dt, "\nversion = \""+targetVersion+"\"\n") {
			//nolint:staticcheck
			return fmt.Errorf("Cargo.toml did not contain %q", targetVersion)
		}
	} else {
		base = base.
			WithSecretVariable("CARGO_REGISTRY_TOKEN", cargoRegistryToken).
			WithExec(args)
	}
	_, err := base.Sync(ctx)
	if err != nil {
		return err
	}

	return nil
}

// Bump the Rust SDK's Engine dependency
func (r RustSDK) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
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

	crate := "dagger-sdk"
	base := r.DevContainer().
		WithExec([]string{
			"cargo", "install", "cargo-edit@" + cargoEditVersion, "--locked",
		}).
		WithExec([]string{
			"cargo", "set-version", "-p", crate, version,
		})

	layer := r.Source().WithNewFile(rustVersionFilePath, versionBumpedContents).
		WithFile(rustCargoTomlFilePath, base.File("Cargo.toml")).
		WithFile(rustCargoLockFilePath, base.File("Cargo.lock"))
	return layer.Changes(r.Source()), nil
}

// Return a Rust dev container with the dagger source mounted and
// the workdir set to ./sdk/rust within it
func (r RustSDK) DevContainer() *dagger.Container {
	// https://hub.docker.com/_/rust
	rustImage := "rust:1.77-bookworm"
	cargoChefVersion := "0.1.62"
	mountPath := "/src"

	// 1. Base image
	ctr := dag.Container().
		From(rustImage).
		WithWorkdir(mountPath).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", dag.CacheVolume("rust-cargo-"+rustImage))

	// 2. Install project dependencies
	srcForInstall := r.Source().Filter(dagger.DirectoryFilterOpts{
		Include: []string{
			"**/Cargo.toml",
			"**/Cargo.lock",
			"**/main.rs",
			"**/lib.rs",
		},
	})
	ctr = ctr.
		WithDirectory(".", srcForInstall).
		WithWorkdir("sdk/rust").
		// combine into one layer so there's no assumptions on state of cache volume across steps
		// FIXME: how can Dagger API be improved to not require this?
		WithExec([]string{"sh", "-c",
			strings.Join([]string{
				"rustup component add rustfmt",
				"cargo install --locked cargo-chef@" + cargoChefVersion,
				"cargo chef prepare --recipe-path /tmp/recipe.json",
				"cargo chef cook --release --workspace --recipe-path /tmp/recipe.json",
			}, "&& "),
		})

	// 3. Mount full source
	return ctr.WithMountedDirectory(mountPath, r.Source())
}
