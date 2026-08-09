// Toolchain to develop and verify the Dagger Rust SDK.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"

	"dagger/rust-sdk-dev/internal/dagger"
)

const (
	rustSdkImage         = "rust:1.97.1-bookworm"
	rustSdkImageDigest   = "sha256:705e294093973d7c10e83400393dce7b3611f8e03e55a80af7fff6d02ae1affb"
	goHelperImage        = "golang:1.26.1-bookworm"
	goHelperDigest       = "sha256:ab3d6955bbc813a0f3fdf220c1d817dd89c0b3f283777db8ece4a32fe7858edd"
	coreTargetRepository = "https://github.com/dagger/dagger.git"
	coreTargetRevision   = "25300124ca110612edc09c43f89cb5fad6028170"
	coreTargetVersion    = "v1.0.0-beta.10"

	rustSdkCrate     = "dagger-sdk"
	cargoEditVersion = "0.13.0"
	cargoChefVersion = "0.1.77"
	cargoDenyVersion = "0.19.9"

	mockCargoRegistryName = "mock"
)

// Develop and verify the Dagger Rust SDK.
type RustSdkDev struct {
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
) *RustSdkDev {
	rustSrc := workspace.Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: []string{
			"*",
			"!sdk/rust/crates",
			"!sdk/rust/completeness",
			"!sdk/rust/examples",
			"!sdk/rust/AGENTS.md",
			"!sdk/rust/CONTRIBUTING.md",
			"!sdk/rust/Cargo.lock",
			"!sdk/rust/Cargo.toml",
			"!sdk/rust/clippy.toml",
			"!sdk/rust/deny.toml",
			"!sdk/rust/rustfmt.toml",
			"!sdk/rust/rust-toolchain.toml",
			"!sdk/go",
			"!cmd/codegen/generator",
			"!core/sdk.go",
			"!core/sdk/go_sdk.go",
			"!core/integration",
			"!internal/cmd/dagger",
			"!internal/version/VERSION",
			"!future/sdk-tests.md",
			"!.kiro/specs/rust-sdk-completeness-contract/requirements.md",
			"!.kiro/specs/rust-sdk-client-lifecycle/requirements.md",
			"!.kiro/specs/rust-sdk-client-lifecycle/design.md",
			"!.kiro/specs/rust-sdk-client-lifecycle/tasks.md",
			"!.kiro/specs/rust-sdk-core-codegen/requirements.md",
			"!.kiro/specs/rust-sdk-transport-observability/requirements.md",
			"!.kiro/specs/rust-sdk-transport-observability/design.md",
			"!.kiro/specs/rust-sdk-transport-observability/tasks.md",
			"!toolchains/rust-sdk-dev/testdata/core_conformance.rs",
		},
	})

	baseContainer := rustBaseContainer().
		// FIXME: not all functions need a full engine build. Do this lazily as needed
		With(func(c *dagger.Container) *dagger.Container {
			return dag.DaggerEngine(dagger.DaggerEngineOpts{
				ClientDockerConfig: clientDockerConfig,
				Ws:                 workspace,
			}).InstallClient(c)
		})

	return &RustSdkDev{
		OriginalWorkspace: rustSrc,
		Workspace:         rustSrc,
		SourcePath:        sourcePath,
		BaseContainer:     baseContainer,
		Ws:                workspace,
	}
}

func rustBaseContainer() *dagger.Container {
	return dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", dag.CacheVolume("rust-cargo-"+rustSdkImage)).
		WithWorkdir("/src")
}

// Return the Rust SDK workspace mounted in a dev container,
// and working directory set to the SDK source.
func (t *RustSdkDev) DevContainer(
	// Install workspace dependencies and any tools required
	// to develop the Rust SDK.
	// +default="false"
	runInstall bool,
) *dagger.Container {
	return t.devContainer(t.BaseContainer, runInstall)
}

func (t *RustSdkDev) devContainer(
	base *dagger.Container,
	runInstall bool,
) *dagger.Container {
	if !runInstall {
		return base.
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

	ctr := base.
		WithDirectory(".", installSrc).
		WithWorkdir(t.SourcePath).
		// combine into one layer so there's no assumptions on state of cache volume across steps
		// FIXME: how can Dagger API be improved to not require this?
		WithExec([]string{
			"sh", "-c",
			strings.Join([]string{
				"rustup component add clippy rustfmt",
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
		WithExec([]string{"cargo", "fmt", "--all", "--check"}).
		Sync(ctx)

	return err
}

// Run cargo check on the Rust SDK
// +check
func (t *RustSdkDev) CargoCheck(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "check", "--workspace", "--all-features", "--release", "--locked"}).
		WithExec([]string{"cargo", "test", "-p", "dagger-sdk", "--all-features", "--test", "public_api", "--locked"}).
		WithExec([]string{"cargo", "test", "-p", "dagger-sdk", "--all-features", "--locked", "public_api_tests::production_request_and_shutdown_sources_pass_the_audit"}).
		Sync(ctx)

	return err
}

// Run Clippy on all Rust SDK targets.
// +check
func (t *RustSdkDev) CargoClippy(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "clippy", "--workspace", "--all-targets", "--all-features", "--locked", "--", "-D", "warnings"}).
		Sync(ctx)

	return err
}

// Build the Rust SDK documentation with warnings denied.
// +check
func (t *RustSdkDev) CargoDoc(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithEnvVariable("RUSTDOCFLAGS", "-D warnings").
		WithExec([]string{"cargo", "doc", "--workspace", "--all-features", "--no-deps", "--locked"}).
		Sync(ctx)

	return err
}

// Check the Rust SDK dependency advisories, licenses, bans, and sources.
// +check
func (t *RustSdkDev) CargoDeny(ctx context.Context) error {
	_, err := t.DevContainer(false).
		WithExec([]string{"cargo", "install", "--locked", "cargo-deny@" + cargoDenyVersion}).
		WithExec([]string{"cargo", "deny", "check"}).
		Sync(ctx)

	return err
}

// Format and lint each standalone Rust SDK example.
// +check
func (t *RustSdkDev) Examples(ctx context.Context) error {
	for _, example := range []string{"examples/backend", "examples/cli", "examples/frontend"} {
		_, err := t.DevContainer(true).
			WithWorkdir(example).
			WithExec([]string{"cargo", "fmt", "--all", "--check"}).
			WithExec([]string{"cargo", "clippy", "--all-targets", "--locked", "--", "-D", "warnings"}).
			Sync(ctx)
		if err != nil {
			return fmt.Errorf("check %s: %w", example, err)
		}
	}

	return nil
}

// Test the Rust SDK
// +check
func (t *RustSdkDev) Test(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"rustc", "--version"}).
		WithExec([]string{"cargo", "test", "--workspace", "--all-features", "--release", "--locked"}).
		WithExec([]string{"cargo", "test", "-p", "dagger-sdk", "--no-default-features", "--test", "raw_only", "--release", "--locked"}).
		Sync(ctx)

	return err
}

// Regenerate the Rust SDK API client.
// +generate
func (t *RustSdkDev) APIClient() *dagger.Changeset {
	return t.WithGeneratedClient().Changes()
}

func (t *RustSdkDev) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t *RustSdkDev) WithGeneratedClient() *RustSdkDev {
	relLayer := t.DevContainer(true).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-bootstrap", "--bin", "dagger-rust", "--locked", "--",
			"generate", "--workspace", "/src/sdk/rust", "--update",
		}).
		Directory(".").
		Filter(dagger.DirectoryFilterOpts{
			Exclude: []string{"target"},
		})

	// Replace the SDK subtree so removals made by the generator are preserved.
	// Overlaying the layer would silently retain obsolete generated files.
	t.Workspace = t.Workspace.
		WithoutDirectory(t.SourcePath).
		WithDirectory(t.SourcePath, relLayer)

	return t
}

// Verify the complete checked-input generated client in graph-local state.
//
// +check
func (t *RustSdkDev) GeneratedClientCheck(ctx context.Context) error {
	generated := *t
	generated.WithGeneratedClient()

	_, err := generated.DevContainer(true).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-bootstrap", "--bin", "dagger-rust", "--locked", "--",
			"generate", "--workspace", "/src/sdk/rust", "--check",
		}).
		WithExec([]string{
			"cargo", "test", "-p", "dagger-sdk", "--all-features", "--locked", "--test", "core_reachability", "--test", "core_projection",
		}).
		WithExec([]string{
			"cargo", "test", "-p", "dagger-codegen", "--locked", "--test", "compile_projection",
		}).
		WithExec([]string{
			"cargo", "test", "-p", "dagger-sdk-completeness", "--locked", "--test", "core_codegen_binding",
		}).
		WithEnvVariable("RUSTDOCFLAGS", "-D warnings").
		WithExec([]string{
			"cargo", "doc", "-p", "dagger-sdk", "--all-features", "--no-deps", "--locked",
		}).
		Sync(ctx)

	return err
}

// Run focused generated-client observations against the immutable checked engine source.
//
// +check
func (t *RustSdkDev) CoreConformance(ctx context.Context) (string, error) {
	generated := *t
	generated.WithGeneratedClient()
	// Evidence artifacts are derived from this identity, so including them would make
	// the subject self-referential. Bind live evidence to the complete compilable Rust
	// source instead; the manifest separately binds every generated artifact byte.
	subjectSource := generated.Source().Filter(dagger.DirectoryFilterOpts{
		Include: []string{
			"Cargo.lock",
			"Cargo.toml",
			"rust-toolchain.toml",
			"crates/**",
		},
	})
	sourceIdentity, err := subjectSource.Digest(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve generated source identity: %w", err)
	}
	// A Dagger digest algorithm change expires evidence conservatively; domain
	// separation gives the evidence contract one stable external digest syntax.
	subjectDigest := sha256.Sum256([]byte("dagger-rust-sdk-subject-v1\x00" + sourceIdentity))
	generated.Workspace = generated.Workspace.WithFile(
		generated.SourcePath+"/crates/dagger-sdk/examples/core_conformance.rs",
		t.Ws.File("toolchains/rust-sdk-dev/testdata/core_conformance.rs"),
	)

	exactEngine := dag.DaggerEngine(dagger.DaggerEngineOpts{Ws: t.Ws}).
		WithGitSource(coreTargetRepository, coreTargetRevision)
	service := exactEngine.Service("rust-sdk-core-conformance", dagger.DaggerEngineServiceOpts{
		Version: coreTargetVersion,
	})
	runner := exactEngine.InstallClient(generated.devContainer(rustBaseContainer(), true), dagger.DaggerEngineInstallClientOpts{
		Service: service,
		Version: coreTargetVersion,
	})
	evidence := runner.
		WithEnvVariable("DAGGER_RUST_CONFORMANCE_OUTPUT", "/tmp/core-conformance-observations.json").
		WithExec([]string{
			"dagger", "run", "cargo", "run", "-p", "dagger-sdk", "--example", "core_conformance", "--locked",
		}).
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", "dagger-core-conformance-evidence", "--locked", "--",
			"--root", "/src/sdk/rust",
			"--observations", "/tmp/core-conformance-observations.json",
			"--subject-digest", fmt.Sprintf("sha256:%x", subjectDigest),
			"--output", "/tmp/core-conformance-evidence.json",
		}).
		File("/tmp/core-conformance-evidence.json")
	contents, err := evidence.Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("run exact-target core conformance: %w", err)
	}
	return contents, nil
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
		WithExec([]string{"cargo", "publish", "-p", rustSdkCrate, "-v", "--all-features", "--dry-run", "--locked"})

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
	// Current Cargo verifies the publish archive and retains its expanded package, but
	// does not promise to retain the intermediate .crate after a successful dry run.
	// The command outcome plus the expanded manifest below prove both packaging and
	// versioning without coupling this check to an implementation-detail artifact.

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

	// Cargo registry index URL to publish to instead of crates.io.
	// +optional
	cargoRegistryIndex string,
) (err error) {
	version := strings.TrimPrefix(sourceTag, "sdk/rust/")
	versionFlag := strings.TrimPrefix(version, "v")
	if !semver.IsValid(version) {
		return fmt.Errorf("invalid version %q", version)
	}

	ctr := t.releaseContainer(versionFlag)
	args := []string{"cargo", "publish", "-p", rustSdkCrate, "-v", "--all-features", "--locked"}
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

func (t *RustSdkDev) releaseContainer(
	versionFlag string,
) *dagger.Container {
	return t.DevContainer(false).
		WithExec([]string{"cargo", "install", "cargo-edit@" + cargoEditVersion, "--locked"}).
		WithExec([]string{"cargo", "set-version", "-p", rustSdkCrate, versionFlag})
}
