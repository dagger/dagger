// Toolchain to develop the Dagger Rust SDK (experimental)
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"

	"dagger/rust-client-dev/internal/dagger"
)

const (
	rustSdkImage       = "rust:1.77-bookworm"
	rustSdkImageDigest = "sha256:83101f6985c93e1e6501b3375de188ee3d2cbb89968bcc91611591f9f447bd42"

	rustGeneratedClientFilePath = "crates/dagger-sdk/src/gen.rs"

	rustSdkCrate     = "dagger-sdk"
	cargoEditVersion = "0.13.0"
	cargoChefVersion = "0.1.62"

	mockCargoRegistryName = "mock"
)

// Develop the Dagger Rust SDK (experimental)
type RustClientDev struct {
	OriginalWorkspace *dagger.Directory // +private
	Workspace         *dagger.Directory // +private
	SourcePath        string            // +private
	BaseContainer     *dagger.Container
	Ws                *dagger.Workspace // +private
}

func New(
	// A directory with all the files needed to develop the SDK
	workspace *dagger.Workspace,
	// The path of the SDK source in the workspace
	// +default="sdk/rust"
	sourcePath string,
	// A docker config file with credentials to install on clients.
	// +optional
	clientDockerConfig *dagger.Secret,
) *RustClientDev {
	rustSrc := workspace.Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: []string{"*", "!sdk/rust/crates", "!sdk/rust/Cargo.lock", "!sdk/rust/Cargo.toml"},
	})

	baseContainer := rustContainer().
		// FIXME: not all functions need a full engine build. Do this lazily as needed
		With(func(c *dagger.Container) *dagger.Container {
			return dag.DaggerEngine(workspace, dagger.DaggerEngineOpts{
				ClientDockerConfig: clientDockerConfig,
			}).InstallClient(dagger.DaggerEngineInstallClientOpts{Client: c})
		})

	return &RustClientDev{
		OriginalWorkspace: rustSrc,
		Workspace:         rustSrc,
		SourcePath:        sourcePath,
		BaseContainer:     baseContainer,
		Ws:                workspace,
	}
}

// Return the Rust SDK workspace mounted in a dev container,
// and working directory set to the SDK source.
func (t *RustClientDev) DevContainer(
	// Install workspace dependencies and any tools required
	// to develop the Rust SDK.
	// +default="false"
	runInstall bool,
) *dagger.Container {
	if !runInstall {
		return t.BaseContainer.
			WithMountedDirectory(".", t.Workspace).
			WithWorkdir(t.SourcePath)
	}

	// Source for installation (without code) to benefit
	// from caches.
	installSrc := t.Workspace.Filter(dagger.DirectoryFilterOpts{
		Include: []string{
			"**/Cargo.toml",
			"**/Cargo.lock",
			"**/main.rs",
			"**/lib.rs",
		},
	})

	ctr := t.BaseContainer.
		WithDirectory(".", installSrc).
		WithWorkdir(t.SourcePath).
		// combine into one layer so there's no assumptions on state of cache volume across steps
		// FIXME: how can Dagger API be improved to not require this?
		WithExec([]string{
			"sh", "-c",
			strings.Join([]string{
				"rustup component add rustfmt",
				"cargo install --locked cargo-chef@" + cargoChefVersion,
				"cargo chef prepare --recipe-path /tmp/recipe.json",
				"cargo chef cook --release --workspace --recipe-path /tmp/recipe.json",
			}, " && "),
		}).
		// Mount back the full source
		WithMountedDirectory("/src", t.Workspace)

	return ctr
}

// Source returns the source directory for the Rust SDK.
func (t *RustClientDev) Source() *dagger.Directory {
	return t.Workspace.Directory(t.SourcePath)
}

// Run cargo fmt on the Rust SDK
// +check
func (t *RustClientDev) CargoFmt(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "fmt", "--check"}).
		Sync(ctx)

	return err
}

// Run cargo check on the Rust SDK
// +check
func (t *RustClientDev) CargoCheck(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "check", "--all", "--release"}).
		Sync(ctx)

	return err
}

// Test the Rust SDK
// +check
func (t *RustClientDev) Test(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"rustc", "--version"}).
		WithExec([]string{"cargo", "test", "--release", "--all"}).
		Sync(ctx)

	return err
}

// Regenerate the Rust SDK API client.
// +generate
func (t *RustClientDev) APIClient() *dagger.Changeset {
	return t.WithGeneratedClient().Changes()
}

func (t *RustClientDev) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t *RustClientDev) WithGeneratedClient() *RustClientDev {
	relLayer := t.DevContainer(true).
		WithMountedFile("/introspection.json", dag.DaggerEngine(t.Ws).IntrospectionJSON()).
		WithExec([]string{"cargo", "run", "-p", "dagger-bootstrap", "generate", "/introspection.json", "--output", rustGeneratedClientFilePath}).
		WithExec([]string{"cargo", "fix", "--all", "--allow-no-vcs"}).
		WithExec([]string{"cargo", "fmt"}).
		Directory(".").
		Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{"target"},
		})

	t.Workspace = t.Workspace.
		// Merge rel layer inside the current workspace (excluding target)
		WithDirectory(t.SourcePath, relLayer)

	return t
}

// Test the publishing process
// +check
func (t *RustClientDev) ReleaseDryRun(
	ctx context.Context,

	// Source git tag to fake-release
	// +default="HEAD"
	sourceTag string,
) (err error) {
	version := strings.TrimPrefix(sourceTag, "sdk/rust/")
	versionFlag := strings.TrimPrefix(version, "v")
	targetVersion := versionFlag
	if !semver.IsValid(version) {
		// just pick any version, it's a dry-run
		versionFlag = "--bump=rc"
	}

	base := t.releaseContainer(versionFlag).
		WithExec([]string{"cargo", "publish", "-p", rustSdkCrate, "-v", "--all-features", "--dry-run"})

	// if the version is not a valid semver, use the one from the Cargo.toml
	// to compare with.
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
	_, err = base.Directory(fmt.Sprintf("./target/package/dagger-sdk-%s", targetVersion)).Sync(ctx)
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

	_, err = base.Sync(ctx)
	if err != nil {
		return fmt.Errorf("failed to run test release: %w", err)
	}

	return nil
}

// Release the Rust SDK
func (t *RustClientDev) Release(
	ctx context.Context,

	// Source git tag to release
	sourceTag string,

	cargoRegistryToken *dagger.Secret,

	// Cargo registry index URL to publish to instead of crates.io.
	// +optional
	cargoRegistryIndex string,
) (err error) {
	version := strings.TrimPrefix(sourceTag, "sdk/rust/")
	versionFlag := strings.TrimPrefix(version, "v")
	if !semver.IsValid(version) {
		return fmt.Errorf("invalid version %q", version)
	}

	ctr := t.releaseContainer(versionFlag).
		WithMountedFile(verifiedCratePath, t.verifiedCrate(versionFlag))
	args := []string{"sh", "-ec", publishVerifiedCrateScript, "sh", versionFlag}
	if cargoRegistryIndex != "" {
		// Cargo alternate registries are configured through
		// CARGO_REGISTRIES_<NAME>_* environment variables.
		ctr = ctr.
			WithEnvVariable("CARGO_REGISTRIES_MOCK_INDEX", cargoRegistryIndex).
			WithSecretVariable("CARGO_REGISTRIES_MOCK_TOKEN", cargoRegistryToken)
		args = append(args, "--registry", mockCargoRegistryName)
	} else {
		ctr = ctr.WithSecretVariable("CARGO_REGISTRY_TOKEN", cargoRegistryToken)
	}

	_, err = ctr.
		WithExec(args).
		Sync(ctx)

	return err
}

const (
	verifiedCratePath = "/verified.crate"

	// Package and verify the crate the way cargo publish does, keeping only
	// the .crate: the verify build in target/ would make the cached result
	// far larger than the crate.
	verifyCrateScript = `cargo package -p ` + rustSdkCrate + ` -v --all-features
mkdir -p /crate
cp "target/package/` + rustSdkCrate + `-$1.crate" /crate/
rm -rf target
`

	// Upload the crate verified by verifyCrateScript. cargo publish always
	// packages the crate again, so package it first and refuse to upload if
	// the result differs from the verified crate.
	publishVerifiedCrateScript = `version=$1
shift
cargo package -p ` + rustSdkCrate + ` --all-features --no-verify
verified=$(sha256sum ` + verifiedCratePath + ` | cut -d' ' -f1)
packaged=$(sha256sum "target/package/` + rustSdkCrate + `-$version.crate" | cut -d' ' -f1)
if [ "$packaged" != "$verified" ]; then
	echo "packaged crate differs from the verified crate: $packaged != $verified" >&2
	exit 1
fi
echo "uploading ` + rustSdkCrate + `-$version.crate, sha256 $verified"
exec cargo publish -p ` + rustSdkCrate + ` -v --all-features --no-verify "$@"
`
)

// verifiedCrate packages and verifies the Rust SDK crate. It needs no
// registry or token, so unlike the upload it can be cached.
func (t *RustClientDev) verifiedCrate(versionFlag string) *dagger.File {
	return t.releaseContainer(versionFlag).
		WithExec([]string{"sh", "-ec", verifyCrateScript, "sh", versionFlag}).
		File("/crate/" + rustSdkCrate + "-" + versionFlag + ".crate")
}

// releaseContainer has no Dagger engine: no release step calls Dagger, and
// the engine service's random state volume would keep every step in it from
// ever being cached.
func (t *RustClientDev) releaseContainer(
	versionFlag string,
) *dagger.Container {
	return rustContainer().
		// Install outside CARGO_HOME: it is a cache volume, which a cached
		// install would not bring back.
		WithExec([]string{"cargo", "install", "--locked", "--root", "/usr/local", "cargo-edit@" + cargoEditVersion}).
		WithMountedDirectory(".", t.Workspace).
		WithWorkdir(t.SourcePath).
		WithExec([]string{"cargo", "set-version", "-p", rustSdkCrate, versionFlag})
}

func rustContainer() *dagger.Container {
	return dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", dag.CacheVolume("rust-cargo-"+rustSdkImage)).
		WithWorkdir("/src")
}
