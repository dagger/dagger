// Toolchain to develop and verify the Dagger Rust SDK.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"

	"dagger/rust-sdk-dev/internal/dagger"
)

const (
	rustSdkImage            = "rust:1.97.1-bookworm"
	rustSdkImageDigest      = "sha256:705e294093973d7c10e83400393dce7b3611f8e03e55a80af7fff6d02ae1affb"
	goHelperImage           = "golang:1.26.1-bookworm"
	goHelperDigest          = "sha256:ab3d6955bbc813a0f3fdf220c1d817dd89c0b3f283777db8ece4a32fe7858edd"
	coreTargetRepository    = "https://github.com/dagger/dagger.git"
	coreTargetRevision      = "25300124ca110612edc09c43f89cb5fad6028170"
	coreTargetVersion       = "v1.0.0-beta.10"
	focusedEngineBaseImage  = "registry.dagger.io/engine:v1.0.0-beta.9@sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e"
	focusedEngineBaseCommit = "1c6e07b197327c57e9db8584deb36e5166278677"
	defaultEngineRepository = "https://github.com/dagger/dagger"

	rustSdkCrate     = "dagger-sdk"
	cargoEditVersion = "0.13.0"
	cargoChefVersion = "0.1.77"
	cargoDenyVersion = "0.19.9"

	mockCargoRegistryName = "mock"
)

// Develop and verify the Dagger Rust SDK.
type RustSdkDev struct {
	OriginalWorkspace  *dagger.Directory // +private
	Workspace          *dagger.Directory // +private
	SourcePath         string            // +private
	BaseContainer      *dagger.Container
	Ws                 *dagger.Workspace // +private
	ClientDockerConfig *dagger.Secret    // +private
	EngineRepository   string            // +private
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
	// Credential-free HTTPS repository that owns the engine source revision.
	// +default="https://github.com/dagger/dagger"
	engineRepository string,
) *RustSdkDev {
	if engineRepository == "" {
		engineRepository = defaultEngineRepository
	}
	rustSrc := workspace.Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: []string{
			"*",
			"!LICENSE",
			"!.github/workflows/rust-sdk-security.yml",
			"!sdk/rust/crates",
			"!sdk/rust/completeness",
			"!sdk/rust/examples",
			"!sdk/rust/runtime",
			// Example workspaces have independent Cargo targets which the repository
			// ignore file cannot describe as one root build directory.
			"sdk/rust/target",
			"sdk/rust/**/target",
			"!sdk/rust/AGENTS.md",
			"!sdk/rust/ARCHITECTURE.md",
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
			"!core/sdk/**",
			"!core/integration",
			"!engine/distconsts/consts.go",
			"!internal/cmd/dagger",
			"!internal/version/VERSION",
			"!toolchains/engine-dev/build/**",
			"!future/sdk-tests.md",
			"!.kiro/specs/rust-sdk-completeness-contract/requirements.md",
			"!.kiro/specs/rust-sdk-client-lifecycle/requirements.md",
			"!.kiro/specs/rust-sdk-client-lifecycle/design.md",
			"!.kiro/specs/rust-sdk-client-lifecycle/tasks.md",
			"!.kiro/specs/rust-sdk-core-codegen/requirements.md",
			"!.kiro/specs/rust-sdk-transport-observability/requirements.md",
			"!.kiro/specs/rust-sdk-transport-observability/design.md",
			"!.kiro/specs/rust-sdk-transport-observability/tasks.md",
			"!.kiro/specs/rust-sdk-engine-integration/requirements.md",
			"!.kiro/specs/rust-sdk-engine-integration/design.md",
			"!.kiro/specs/rust-sdk-engine-integration/tasks.md",
			"!toolchains/rust-sdk-dev/**",
		},
	})

	return &RustSdkDev{
		OriginalWorkspace:  rustSrc,
		Workspace:          rustSrc,
		SourcePath:         sourcePath,
		BaseContainer:      rustBaseContainer(),
		Ws:                 workspace,
		ClientDockerConfig: clientDockerConfig,
		EngineRepository:   engineRepository,
	}
}

func rustBaseContainer() *dagger.Container {
	return dag.Container().
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", dag.CacheVolume("rust-cargo-"+rustSdkImage)).
		WithWorkdir("/src")
}

// focusedEngineSource is the complete source closure for the changing engine,
// CLI, and Rust integration. Keeping the distribution's other SDKs and local
// build products outside this boundary prevents unrelated bytes from invalidating
// the development engine while retaining every source package used by the two Go
// binaries.
func (t *RustSdkDev) focusedEngineSource() *dagger.Directory {
	return t.Ws.Directory("/", dagger.WorkspaceDirectoryOpts{Exclude: []string{
		"*",
		"!LICENSE",
		"!go.mod",
		"!go.sum",
		"!analytics",
		"!auth",
		"!cmd",
		"!core",
		"!dagql",
		"!engine",
		"!internal",
		"!network",
		"!util",
		// The root module replaces dagger.io/dagger with this local module. Go must
		// resolve the replacement even though only the engine and CLI are compiled.
		"!sdk/go/**",
		"!sdk/rust/Cargo.lock",
		"!sdk/rust/Cargo.toml",
		"!sdk/rust/rust-toolchain.toml",
		"!sdk/rust/completeness/target.json",
		"!sdk/rust/completeness/snapshots/schema.json",
		"!sdk/rust/crates/**/Cargo.toml",
		"!sdk/rust/crates/**/src/**",
		"!sdk/rust/crates/**/assets/**",
		"!sdk/rust/runtime/**",
	}})
}

// engineClientContainer is the explicit engine-bearing boundary reserved for engine-content and
// exact-target integration functions. Ordinary Rust checks must continue from BaseContainer so a
// schema, documentation, or unit-test edit cannot silently trigger an engine build.
func (t *RustSdkDev) engineClientContainer(base *dagger.Container) *dagger.Container {
	return dag.DaggerEngine(dagger.DaggerEngineOpts{
		ClientDockerConfig: t.ClientDockerConfig,
		Ws:                 t.Ws,
	}).InstallClient(base)
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

// Run the focused Rust engine-tool and adapter unit suite without constructing an engine.
//
// Tests are mounted only after dependency installation, so test-only edits do not invalidate the
// compiler/toolchain and dependency layer shared by later engine-content checkpoints.
//
// +check
func (t *RustSdkDev) EngineUnit(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{
			"cargo", "test", "-p", "dagger-sdk-engine", "--all-targets", "--locked",
		}).
		WithExec([]string{
			"cargo", "test", "-p", "dagger-sdk-completeness", "--test", "engine_integration", "--locked",
		}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("run focused Rust engine unit tests: %w", err)
	}

	goRoot := t.Ws.Directory("/")
	_, err = dag.Container().
		From(goHelperImage+"@"+goHelperDigest).
		WithMountedDirectory("/src", goRoot).
		WithWorkdir("/src").
		WithExec([]string{"go", "test", "./core/sdk", "./core/sdk/sdkmeta", "./core/schema"}).
		// The full CLI package suite provisions the released CLI. Rust adapter validation
		// owns only SDK install resolution, so keep it deterministic and offline by selecting
		// the production resolver table directly.
		WithExec([]string{"go", "test", "./internal/cmd/dagger", "-run", "^TestSDKResolveInstall$"}).
		WithWorkdir("/src/sdk/rust/runtime").
		WithExec([]string{"go", "test", "./internal/metadata"}).
		WithWorkdir("/src/toolchains/rust-sdk-dev").
		WithExec([]string{"go", "test", "./internal/enginefree"}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("run focused Rust SDK adapter tests: %w", err)
	}
	return nil
}

// RustEngineContent retains one engine-dev content object with both identities
// needed to prove the acyclic packaged-content boundary.
type RustEngineContent struct {
	Content          *dagger.Directory
	ManifestDigest   string
	DescriptorDigest string
	Engine           *dagger.DaggerEngine                  // +private
	Built            *dagger.DaggerEngineRustEngineContent // +private
}

// EngineContent builds the Rust SDK content once and returns its reusable graph object.
func (t *RustSdkDev) EngineContent(ctx context.Context) (*RustEngineContent, error) {
	engine := dag.DaggerEngine(dagger.DaggerEngineOpts{
		ClientDockerConfig: t.ClientDockerConfig,
		Ws:                 t.Ws,
		VcsRepository:      t.EngineRepository,
	}).WithSource(t.focusedEngineSource())
	built := engine.RustSdkcontent(dagger.DaggerEngineRustSdkcontentOpts{
		Version: coreTargetVersion,
	})
	manifestDigest, err := built.ManifestDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve Rust SDK OCI manifest identity: %w", err)
	}
	descriptorDigest, err := built.DescriptorDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve Rust SDK descriptor identity: %w", err)
	}
	if !isCanonicalSHA256(manifestDigest) || !isCanonicalSHA256(descriptorDigest) {
		return nil, fmt.Errorf("Rust SDK content returned malformed manifest or descriptor identity")
	}
	return &RustEngineContent{
		Content:          built.Content(),
		ManifestDigest:   manifestDigest,
		DescriptorDigest: descriptorDigest,
		Engine:           engine,
		Built:            built,
	}, nil
}

// Resolution starts an exact-target engine from this object's content and exercises
// the built-in loader and workspace install path without reconstructing that content.
func (content *RustEngineContent) Resolution(ctx context.Context) (string, error) {
	if content.Engine == nil || content.Built == nil {
		return "", fmt.Errorf("Rust SDK content is detached from its engine construction graph")
	}
	service := content.Engine.ServiceWithFocusedRustSdkcontent(
		content.Built,
		"rust-sdk-resolution",
		focusedEngineBaseImage,
		focusedEngineBaseCommit,
		coreTargetRepository,
		coreTargetRevision,
		dagger.DaggerEngineServiceWithFocusedRustSdkcontentOpts{Version: coreTargetVersion},
	)
	runner := content.Engine.InstallClient(
		dag.Container().
			From(goHelperImage+"@"+goHelperDigest).
			WithDirectory("/work", dag.Directory()).
			WithWorkdir("/work").
			WithExec([]string{"git", "init"}).
			WithExec([]string{"git", "config", "user.name", "Rust SDK Check"}).
			WithExec([]string{"git", "config", "user.email", "rust-sdk-check@dagger.invalid"}).
			WithExec([]string{"git", "commit", "--allow-empty", "-m", "initialize workspace"}),
		dagger.DaggerEngineInstallClientOpts{
			Service: service,
			Version: coreTargetVersion,
		},
	)
	first := runner.WithExec([]string{"dagger", "-y", "sdk", "install", "--here", "rust"})
	firstConfig, err := first.File("/work/dagger.toml").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("install bare Rust SDK: %w", err)
	}
	second := first.WithExec([]string{"dagger", "-y", "sdk", "install", "--here", "rust"})
	secondConfig, err := second.File("/work/dagger.toml").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("reinstall bare Rust SDK: %w", err)
	}
	if firstConfig != secondConfig {
		return "", fmt.Errorf("bare Rust SDK reinstall changed workspace configuration")
	}
	installed, err := second.WithExec([]string{"dagger", "sdk", "installed"}).Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("list installed Rust SDK: %w", err)
	}
	if !strings.Contains(installed, "rust") {
		return "", fmt.Errorf("installed SDK listing omitted canonical Rust entry")
	}
	rejected := second.WithExec(
		[]string{"dagger", "-y", "sdk", "install", "rust@v1.0.0-beta.10"},
		dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny},
	)
	exitCode, err := rejected.ExitCode(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect Rust shorthand rejection: %w", err)
	}
	if exitCode == 0 {
		return "", fmt.Errorf("versioned Rust built-in unexpectedly reached external resolution")
	}
	stderr, err := rejected.Stderr(ctx)
	if err != nil {
		return "", fmt.Errorf("read Rust shorthand rejection: %w", err)
	}
	if !strings.Contains(stderr, "does not currently support selecting a specific version") {
		return "", fmt.Errorf("versioned Rust built-in failed without the stable pre-fallback diagnostic")
	}
	evidence, err := json.Marshal(struct {
		DescriptorDigest  string `json:"descriptor_digest"`
		Installed         bool   `json:"installed"`
		ManifestDigest    string `json:"manifest_digest"`
		ReinstallNoop     bool   `json:"reinstall_noop"`
		ShorthandRejected bool   `json:"shorthand_rejected"`
	}{
		DescriptorDigest:  content.DescriptorDigest,
		Installed:         true,
		ManifestDigest:    content.ManifestDigest,
		ReinstallNoop:     true,
		ShorthandRejected: true,
	})
	if err != nil {
		return "", fmt.Errorf("encode Rust SDK resolution evidence: %w", err)
	}
	return string(evidence), nil
}

// EngineIntegration exercises the initialization, operation, and runtime boundaries
// against one exact-target engine and one previously built Rust SDK content object.
func (content *RustEngineContent) EngineIntegration(
	ctx context.Context,
	// Focused case names; an empty list runs the complete checkpoint.
	cases []string,
) (string, error) {
	if content.Engine == nil || content.Built == nil {
		return "", fmt.Errorf("Rust SDK content is detached from its engine construction graph")
	}
	if len(cases) == 0 {
		cases = []string{
			"init-empty", "init-existing", "init-no-generate", "operations",
			"runtime-checked", "runtime-legacy",
		}
	}
	allowed := map[string]struct{}{
		"init-empty": {}, "init-existing": {}, "init-no-generate": {},
		"operations": {}, "runtime-checked": {}, "runtime-legacy": {},
	}
	seen := make(map[string]struct{}, len(cases))
	for _, name := range cases {
		if _, ok := allowed[name]; !ok {
			return "", fmt.Errorf("unknown Rust engine-integration case %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return "", fmt.Errorf("duplicate Rust engine-integration case %q", name)
		}
		seen[name] = struct{}{}
	}

	service := content.Engine.ServiceWithFocusedRustSdkcontent(
		content.Built,
		"rust-sdk-engine-integration",
		focusedEngineBaseImage,
		focusedEngineBaseCommit,
		coreTargetRepository,
		coreTargetRevision,
		dagger.DaggerEngineServiceWithFocusedRustSdkcontentOpts{Version: coreTargetVersion},
	)
	observations := make(map[string]string, len(cases))
	for _, name := range cases {
		runner := content.integrationRunner(service, name)
		identity, err := content.runEngineIntegrationCase(ctx, runner, name)
		if err != nil {
			return "", fmt.Errorf("run Rust engine-integration case %s: %w", name, err)
		}
		observations[name] = identity
	}
	evidence, err := json.Marshal(struct {
		Cases            map[string]string `json:"cases"`
		DescriptorDigest string            `json:"descriptor_digest"`
		ManifestDigest   string            `json:"manifest_digest"`
	}{
		Cases: observations, DescriptorDigest: content.DescriptorDigest,
		ManifestDigest: content.ManifestDigest,
	})
	if err != nil {
		return "", fmt.Errorf("encode Rust engine-integration evidence: %w", err)
	}
	return string(evidence), nil
}

func (content *RustEngineContent) integrationRunner(
	service *dagger.Service,
	name string,
) *dagger.Container {
	base := dag.Container().
		From(goHelperImage+"@"+goHelperDigest).
		WithDirectory("/work", dag.Directory()).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"}).
		WithExec([]string{"git", "config", "user.name", "Rust SDK Check"}).
		WithExec([]string{"git", "config", "user.email", "rust-sdk-check@dagger.invalid"}).
		WithExec([]string{"git", "commit", "--allow-empty", "-m", "initialize workspace"}).
		WithEnvVariable("RUST_SDK_ENGINE_INTEGRATION_CASE", name)
	return content.Engine.InstallClient(base, dagger.DaggerEngineInstallClientOpts{
		Service: service, Version: coreTargetVersion,
	})
}

func (content *RustEngineContent) runEngineIntegrationCase(
	ctx context.Context,
	runner *dagger.Container,
	name string,
) (string, error) {
	installed := runner.WithExec([]string{"dagger", "-y", "sdk", "install", "--here", "rust"})
	switch name {
	case "init-empty":
		result := installed.WithExec([]string{
			"dagger", "-y", "module", "init", "rust", "empty", "--path", "modules/empty",
		})
		if err := requirePaths(ctx, result.Directory("/work"), []string{
			"modules/empty/Cargo.toml", "modules/empty/Cargo.lock",
			"modules/empty/rust-toolchain.toml", "modules/empty/src/lib.rs",
			"modules/empty/.dagger/rust/operation-manifest.json",
			"modules/empty/src/bin/dagger-module.rs",
		}); err != nil {
			return "", err
		}
		return result.Directory("/work/modules/empty").Digest(ctx)
	case "init-existing":
		const callerSource = "//! caller-owned source\npub fn caller_owned() {}\n"
		result := installed.
			WithNewFile("/work/modules/existing/Cargo.toml", "[package]\nname = \"existing\"\nversion = \"0.1.0\"\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\nserde = \"1\"\n").
			WithNewFile("/work/modules/existing/src/lib.rs", callerSource).
			WithExec([]string{
				"dagger", "-y", "module", "init", "rust", "existing", "--path", "modules/existing",
			})
		contents, err := result.File("/work/modules/existing/src/lib.rs").Contents(ctx)
		if err != nil {
			return "", fmt.Errorf("read preserved authored source: %w", err)
		}
		if contents != callerSource {
			return "", fmt.Errorf("Rust initialization changed caller-owned source")
		}
		manifest, err := result.File("/work/modules/existing/Cargo.toml").Contents(ctx)
		if err != nil {
			return "", fmt.Errorf("read adopted Cargo manifest: %w", err)
		}
		if !strings.Contains(manifest, "serde = \"1\"") || !strings.Contains(manifest, "dagger-sdk") {
			return "", fmt.Errorf("Rust initialization did not preserve and adopt the Cargo manifest")
		}
		return result.Directory("/work/modules/existing").Digest(ctx)
	case "init-no-generate":
		result := installed.WithExec([]string{
			"dagger", "-y", "module", "init", "rust", "ungenerated", "--path", "modules/ungenerated", "--no-generate",
		})
		if err := requirePaths(ctx, result.Directory("/work"), []string{
			"modules/ungenerated/Cargo.toml", "modules/ungenerated/Cargo.lock",
			"modules/ungenerated/src/lib.rs",
		}); err != nil {
			return "", err
		}
		exists, err := result.Directory("/work").Exists(ctx, "modules/ungenerated/.dagger/rust/operation-manifest.json")
		if err != nil {
			return "", fmt.Errorf("inspect no-generate output: %w", err)
		}
		if exists {
			return "", fmt.Errorf("--no-generate published a Rust operation manifest")
		}
		return result.Directory("/work/modules/ungenerated").Digest(ctx)
	case "operations":
		result := installed.WithExec([]string{"dagger", "-y", "module", "init", "rust", "operations"})
		workspaceConfig, err := result.File("/work/dagger.toml").Contents(ctx)
		if err != nil {
			return "", fmt.Errorf("read operations workspace config: %w", err)
		}
		// Client initialization is an optional SDK surface. Registering the client
		// directly proves the mandatory GenerateClient hook without conflating it
		// with an initializer that this SDK does not advertise.
		workspaceConfig += "\n[[modules.dagger-rust-sdk.as-sdk.clients]]\n" +
			"path = \"clients/rust\"\n" +
			"module = \".dagger/modules/operations\"\n"
		result = result.
			WithNewFile("/work/dagger.toml", workspaceConfig).
			WithExec([]string{"dagger", "-y", "generate"})
		if err := requirePaths(ctx, result.Directory("/work"), []string{
			".dagger/modules/operations/.dagger/rust/operation-manifest.json",
			".dagger/modules/operations/src/dagger_generated/mod.rs",
			"clients/rust/Cargo.toml", "clients/rust/src/lib.rs",
			"clients/rust/src/dagger_generated/mod.rs",
		}); err != nil {
			return "", err
		}
		return result.Directory("/work").Digest(ctx)
	case "runtime-checked":
		result := installed.
			WithExec([]string{"dagger", "-y", "module", "init", "rust", "runtime-checked"})
		functions, err := result.WithExec([]string{
			"dagger", "-m", ".dagger/modules/runtime-checked", "functions",
		}).Stdout(ctx)
		if err != nil {
			return "", fmt.Errorf("load checked Rust runtime: %w", err)
		}
		if !strings.Contains(functions, "probe") {
			return "", fmt.Errorf("checked Rust runtime omitted the fixed protocol surface")
		}
		return result.Directory("/work/.dagger/modules/runtime-checked").Digest(ctx)
	case "runtime-legacy":
		result := installed.
			WithExec([]string{
				"dagger", "-y", "module", "init", "rust", "runtime-legacy", "--path", "modules/runtime-legacy", "--no-generate",
			}).
			WithExec([]string{"rm", "modules/runtime-legacy/dagger-module.toml"}).
			WithNewFile("/work/modules/runtime-legacy/dagger.json", `{"name":"runtime-legacy","engineVersion":"v1.0.0-beta.10","sdk":{"source":"rust"}}`)
		functions, err := result.WithExec([]string{
			"dagger", "-m", "modules/runtime-legacy", "functions",
		}).Stdout(ctx)
		if err != nil {
			return "", fmt.Errorf("load legacy Rust runtime: %w", err)
		}
		if !strings.Contains(functions, "probe") {
			return "", fmt.Errorf("legacy Rust runtime omitted the fixed protocol surface")
		}
		generatedOnHost, err := result.Directory("/work").Exists(ctx, "modules/runtime-legacy/.dagger/rust/operation-manifest.json")
		if err != nil {
			return "", fmt.Errorf("inspect legacy host source: %w", err)
		}
		if generatedOnHost {
			return "", fmt.Errorf("legacy runtime generation escaped its private snapshot")
		}
		return result.Directory("/work/modules/runtime-legacy").Digest(ctx)
	default:
		return "", fmt.Errorf("unreachable Rust engine-integration case %q", name)
	}
}

func requirePaths(ctx context.Context, root *dagger.Directory, paths []string) error {
	for _, candidate := range paths {
		exists, err := root.Exists(ctx, candidate)
		if err != nil {
			return fmt.Errorf("inspect required path %s: %w", candidate, err)
		}
		if !exists {
			manifests, globErr := root.Glob(ctx, "**/.dagger/rust/operation-manifest.json")
			if globErr != nil {
				return fmt.Errorf("required path %s is absent; inspect operation manifests: %w", candidate, globErr)
			}
			return fmt.Errorf("required path %s is absent; operation manifests: %v", candidate, manifests)
		}
	}
	return nil
}

func isCanonicalSHA256(value string) bool {
	algorithm, encoded, found := strings.Cut(value, ":")
	if !found || algorithm != "sha256" || len(encoded) != 64 {
		return false
	}
	for _, character := range encoded {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
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
