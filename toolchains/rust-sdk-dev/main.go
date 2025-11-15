package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"

	"dagger/rust-sdk-dev/internal/dagger"
)

const (
	rustSdkImage       = "rust:1.77-bookworm"
	rustSdkImageDigest = "sha256:83101f6985c93e1e6501b3375de188ee3d2cbb89968bcc91611591f9f447bd42"

	rustVersionFilePath         = "crates/dagger-sdk/src/core/version.rs"
	rustCargoTomlFilePath       = "Cargo.toml"
	rustCargoLockFilePath       = "Cargo.lock"
	rustGeneratedClientFilePath = "crates/dagger-sdk/src/gen.rs"

	rustSdkCrate     = "dagger-sdk"
	cargoEditVersion = "0.13.0"
	cargoChefVersion = "0.1.62"
)

// Develop the Dagger Rust SDK (experimental)
type RustSdkDev struct {
	OriginalWorkspace *dagger.Directory // +private
	Workspace         *dagger.Directory // +private
	SourcePath        string            // +private
	BaseContainer     *dagger.Container
}

func New(
	// A directory with all the files needed to develop the SDK
	// +defaultPath="/"
	// +ignore=["*", "!sdk/rust/crates", "!sdk/rust/Cargo.lock", "!sdk/rust/Cargo.toml"]
	workspace *dagger.Directory,
	// The path of the SDK source in the workspace
	// +default="sdk/rust"
	sourcePath string,
) *RustSdkDev {
	baseContainer := dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", dag.CacheVolume("rust-cargo-"+rustSdkImage)).
		WithWorkdir("/src").
		With(func(c *dagger.Container) *dagger.Container {
			return dag.DaggerEngine().InstallClient(c)
		})

	return &RustSdkDev{
		OriginalWorkspace: workspace,
		Workspace:         workspace,
		SourcePath:        sourcePath,
		BaseContainer:     baseContainer,
	}
}

// Return the Rust SDK workspace mounted in a dev container,
// and working directory set to the SDK source.
func (t *RustSdkDev) DevContainer(
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
func (t *RustSdkDev) Source() *dagger.Directory {
	return t.Workspace.Directory(t.SourcePath)
}

// Run cargo fmt on the Rust SDK
// +check
func (t *RustSdkDev) CargoFmt(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "fmt", "--check"}).
		Sync(ctx)

	return err
}

// Run cargo check on the Rust SDK
// +check
func (t *RustSdkDev) CargoCheck(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "check", "--all", "--release"}).
		Sync(ctx)

	return err
}

// Test the Rust SDK
// +check
func (t *RustSdkDev) Test(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"rustc", "--version"}).
		WithExec([]string{"cargo", "test", "--release", "--all"}).
		Sync(ctx)

	return err
}

// Regenerate the Rust SDK API client.
func (t *RustSdkDev) Generate() *dagger.Changeset {
	return t.WithGeneratedClient().Changes()
}

func (t *RustSdkDev) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t *RustSdkDev) WithGeneratedClient() *RustSdkDev {
	relLayer := t.DevContainer(true).
		WithMountedFile("/introspection.json", dag.DaggerEngine().IntrospectionJSON()).
		WithExec([]string{"cargo", "run", "-p", "dagger-bootstrap", "generate", "/introspection.json", "--output", rustGeneratedClientFilePath}).
		WithExec([]string{"cargo", "fix", "--all", "--allow-no-vcs"}).
		WithExec([]string{"cargo", "fmt"}).
		Directory(".")

	t.Workspace = t.Workspace.
		WithoutDirectory(t.SourcePath).
		WithDirectory(t.SourcePath, relLayer)

	return t
}

// Test the publishing process
// +check
func (t *RustSdkDev) ReleaseDryRun(
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
func (t *RustSdkDev) Release(
	ctx context.Context,

	// Source git tag to release
	sourceTag string,

	cargoRegistryToken *dagger.Secret,
) (err error) {
	version := strings.TrimPrefix(sourceTag, "sdk/rust/")
	versionFlag := strings.TrimPrefix(version, "v")
	if !semver.IsValid(version) {
		return fmt.Errorf("invalid version %q", version)
	}

	_, err = t.releaseContainer(versionFlag).
		WithSecretVariable("CARGO_REGISTRY_TOKEN", cargoRegistryToken).
		WithExec([]string{"cargo", "publish", "-p", rustSdkCrate, "-v", "--all-features"}).
		Sync(ctx)

	return err
}

func (t *RustSdkDev) releaseContainer(
	versionFlag string,
) *dagger.Container {
	return t.DevContainer(false).
		WithExec([]string{"cargo", "install", "cargo-edit@" + cargoEditVersion, "--locked"}).
		WithExec([]string{"cargo", "set-version", "-p", rustSdkCrate, versionFlag})
}

// Bump the Rust SDK's engine dependency version.
func (t *RustSdkDev) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	versionStr := `pub const DAGGER_ENGINE_VERSION: &'static str = "([0-9\.-a-zA-Z]+)";`
	versionStrf := `pub const DAGGER_ENGINE_VERSION: &'static str = "%s";`
	version = strings.TrimPrefix(version, "v")

	versionContents, err := t.Workspace.File(rustVersionFilePath).Contents(ctx)
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

	base := t.DevContainer(false).
		WithExec([]string{
			"cargo", "install", "cargo-edit@" + cargoEditVersion, "--locked",
		}).
		WithExec([]string{
			"cargo", "set-version", "-p", rustSdkCrate, version,
		})

	layer := t.Workspace.WithNewFile(rustVersionFilePath, versionBumpedContents).
		WithFile(rustCargoTomlFilePath, base.File("Cargo.toml")).
		WithFile(rustCargoLockFilePath, base.File("Cargo.lock"))

	return layer.Changes(t.OriginalWorkspace), nil
}
