// Toolchain to develop and verify the Dagger Rust SDK.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"dagger/rust-client-dev/internal/enginefixture"
	"github.com/BurntSushi/toml"

	"dagger/rust-client-dev/internal/dagger"
)

const (
	rustSdkImage            = "rust:1.97.1-bookworm"
	rustToolchainVersion    = "1.97.1"
	rustSdkImageDigest      = "sha256:705e294093973d7c10e83400393dce7b3611f8e03e55a80af7fff6d02ae1affb"
	goHelperImage           = "golang:1.26.1-bookworm"
	goHelperDigest          = "sha256:ab3d6955bbc813a0f3fdf220c1d817dd89c0b3f283777db8ece4a32fe7858edd"
	coreTargetRepository    = "https://github.com/dagger/dagger.git"
	coreTargetRevision      = "501b57e0476dee5881b99a064c3c04173134ecc7"
	coreTargetVersion       = "v1.0.0-beta.11.rust.1"
	focusedEngineBaseImage  = "registry.dagger.io/engine:v1.0.0-beta.9@sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e"
	focusedEngineBaseCommit = "1c6e07b197327c57e9db8584deb36e5166278677"
	defaultEngineRepository = "https://github.com/dagger/dagger"

	rustSdkCrate       = "dagger-sdk"
	rustSdkMacrosCrate = "dagger-sdk-macros"
	cargoChefVersion   = "0.1.77"
	cargoDenyVersion   = "0.19.9"
)

var engineIntegrationCases = []string{
	"resolution",
	"init-empty",
	"init-existing",
	"init-no-generate",
	"operations",
	"runtime-checked",
	"runtime-legacy",
	"negative-generated-lock-toolchain",
	"negative-path-ownership",
	"negative-redaction",
}

const maxEngineIntegrationConcurrency = 4

type sdkDependencyDescriptor struct {
	Source       string `json:"source"`
	Registry     string `json:"registry,omitempty"`
	Package      string `json:"package"`
	ExactVersion string `json:"exact_version,omitempty"`
	URL          string `json:"url,omitempty"`
	Revision     string `json:"revision,omitempty"`
}

// Develop and verify the Dagger Rust SDK.
type RustClientDev struct {
	OriginalWorkspace     *dagger.Directory // +private
	Workspace             *dagger.Directory // +private
	SourcePath            string            // +private
	BaseContainer         *dagger.Container
	Ws                    *dagger.Workspace // +private
	ClientDockerConfig    *dagger.Secret    // +private
	EngineRepository      string            // +private
	SDKDependencyRevision string            // +private
}

func New(
	// Active repository workspace.
	ws *dagger.Workspace,
	// The path of the SDK source in the workspace
	// +default="sdk/rust"
	sourcePath string,
	// A docker config file with credentials to install on clients.
	// +optional
	clientDockerConfig *dagger.Secret,
	// Credential-free HTTPS repository that owns the engine source revision.
	// +default="https://github.com/dagger/dagger"
	engineRepository string,
	// Full reachable revision in the engine repository containing the public dagger-sdk package.
	// +optional
	sdkDependencyRevision string,
) *RustClientDev {
	if engineRepository == "" {
		engineRepository = defaultEngineRepository
	}
	rustSrc := ws.Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: []string{
			"*",
			"!LICENSE",
			"!sdk/rust/crates",
			"!sdk/rust/codegen",
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
			"!.dagger/modules/engine-dev/build/**",
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
			"!.dagger/modules/rust-client-dev/**",
		},
	})

	return &RustClientDev{
		OriginalWorkspace:     rustSrc,
		Workspace:             rustSrc,
		SourcePath:            sourcePath,
		BaseContainer:         rustBaseContainer(),
		Ws:                    ws,
		ClientDockerConfig:    clientDockerConfig,
		EngineRepository:      engineRepository,
		SDKDependencyRevision: sdkDependencyRevision,
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
func (t *RustClientDev) focusedEngineSource() *dagger.Directory {
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
		"!sdk/rust/codegen/target.json",
		"!sdk/rust/codegen/schema.json",
		"!sdk/rust/crates/**/Cargo.toml",
		"!sdk/rust/crates/**/src/**",
		"!sdk/rust/crates/**/assets/**",
		"!sdk/rust/runtime/**",
	}})
}

// engineClientContainer is the explicit engine-bearing boundary reserved for engine-content and
// exact-target integration functions. Ordinary Rust checks must continue from BaseContainer so a
// schema, documentation, or unit-test edit cannot silently trigger an engine build.
func (t *RustClientDev) engineClientContainer(base *dagger.Container) *dagger.Container {
	return dag.DaggerEngine(dagger.DaggerEngineOpts{
		ClientDockerConfig: t.ClientDockerConfig,
		Ws:                 t.Ws,
	}).InstallClient(base)
}

// Return the Rust SDK workspace mounted in a dev container,
// and working directory set to the SDK source.
func (t *RustClientDev) DevContainer(
	// Install workspace dependencies and any tools required
	// to develop the Rust SDK.
	// +default="false"
	runInstall bool,
) *dagger.Container {
	return t.devContainer(t.BaseContainer, runInstall)
}

func (t *RustClientDev) devContainer(
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
func (t *RustClientDev) Source() *dagger.Directory {
	return t.Workspace.Directory(t.SourcePath)
}

// Run cargo fmt on the Rust SDK
// +check
func (t *RustClientDev) CargoFmt(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "fmt", "--all", "--check"}).
		Sync(ctx)

	return err
}

// Run cargo check on the Rust SDK
// +check
func (t *RustClientDev) CargoCheck(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "check", "--workspace", "--all-features", "--release", "--locked"}).
		WithExec([]string{"cargo", "test", "-p", "dagger-sdk", "--all-features", "--test", "public_api", "--locked"}).
		WithExec([]string{"cargo", "test", "-p", "dagger-sdk", "--all-features", "--locked", "public_api_tests::production_request_and_shutdown_sources_pass_the_audit"}).
		Sync(ctx)

	return err
}

// Run Clippy on all Rust SDK targets.
// +check
func (t *RustClientDev) CargoClippy(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{"cargo", "clippy", "--workspace", "--all-targets", "--all-features", "--locked", "--", "-D", "warnings"}).
		Sync(ctx)

	return err
}

// Build the Rust SDK documentation with warnings denied.
// +check
func (t *RustClientDev) CargoDoc(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithEnvVariable("RUSTDOCFLAGS", "-D warnings").
		WithExec([]string{"cargo", "doc", "--workspace", "--all-features", "--no-deps", "--locked"}).
		Sync(ctx)

	return err
}

// Check the Rust SDK dependency advisories, licenses, bans, and sources.
// +check
func (t *RustClientDev) CargoDeny(ctx context.Context) error {
	_, err := t.DevContainer(false).
		WithExec([]string{"cargo", "install", "--locked", "cargo-deny@" + cargoDenyVersion}).
		WithExec([]string{"cargo", "deny", "check"}).
		Sync(ctx)

	return err
}

// Format and lint each standalone Rust SDK example.
// +check
func (t *RustClientDev) Examples(ctx context.Context) error {
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
func (t *RustClientDev) Test(ctx context.Context) error {
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
func (t *RustClientDev) EngineUnit(ctx context.Context) error {
	_, err := t.DevContainer(true).
		WithExec([]string{
			"cargo", "test", "-p", "dagger-sdk-engine", "--all-targets", "--locked",
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
		// owns only SDK resolution and its deliberately bounded initializer surface, so keep
		// it deterministic and offline by selecting those production boundaries directly.
		WithExec([]string{
			"go", "test", "./internal/cmd/dagger", "-run",
			"^(TestSDKResolveInstall|TestPackagedRustSDKRegistersOnlyImplementedInitializer)$",
		}).
		WithWorkdir("/src/sdk/rust/runtime").
		WithExec([]string{"go", "test", "./internal/metadata"}).
		WithWorkdir("/src/.dagger/modules/rust-client-dev").
		WithExec([]string{"go", "test", "./internal/enginefree"}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("run focused Rust SDK adapter tests: %w", err)
	}
	return nil
}

// RustEngineContent retains one reusable engine-content graph and its package identities.
type RustEngineContent struct {
	Content          *dagger.Directory
	ManifestDigest   string
	DescriptorDigest string
	Engine           *dagger.DaggerEngine                  // +private
	Built            *dagger.DaggerEngineRustEngineContent // +private
}

// EngineContent builds the Rust SDK content once and returns its reusable graph object.
func (t *RustClientDev) EngineContent(ctx context.Context) (*RustEngineContent, error) {
	engine := dag.DaggerEngine(dagger.DaggerEngineOpts{
		ClientDockerConfig: t.ClientDockerConfig,
		Ws:                 t.Ws,
		VcsRepository:      t.EngineRepository,
	}).WithSource(t.focusedEngineSource())
	contentOptions := dagger.DaggerEngineRustSdkcontentOpts{Version: coreTargetVersion}
	if t.SDKDependencyRevision != "" {
		contentOptions.DependencyRepository = t.EngineRepository
		contentOptions.DependencyRevision = t.SDKDependencyRevision
	}
	built := engine.RustSdkcontent(contentOptions)
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
	dependencyDescriptor, err := built.DependencyDescriptor(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve Rust SDK dependency descriptor: %w", err)
	}
	var sdkDependency sdkDependencyDescriptor
	if err := json.Unmarshal([]byte(dependencyDescriptor), &sdkDependency); err != nil {
		return nil, fmt.Errorf("decode Rust SDK dependency descriptor: %w", err)
	}
	if sdkDependency.Package != rustSdkCrate ||
		(sdkDependency.Source != "registry" && sdkDependency.Source != "git") {
		return nil, fmt.Errorf("Rust SDK content returned an unsupported dependency descriptor")
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
	return content.runResolution(ctx, content.focusedService("rust-sdk-resolution"))
}

func (content *RustEngineContent) focusedService(name string) *dagger.Service {
	return content.Engine.ServiceWithFocusedRustSdkcontent(
		content.Built,
		name,
		focusedEngineBaseImage,
		focusedEngineBaseCommit,
		coreTargetRepository,
		coreTargetRevision,
		dagger.DaggerEngineServiceWithFocusedRustSdkcontentOpts{Version: coreTargetVersion},
	)
}

func (content *RustEngineContent) runResolution(
	ctx context.Context,
	service *dagger.Service,
) (string, error) {
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
		[]string{"dagger", "-y", "sdk", "install", "rust@v1.0.0-beta.11.rust.1"},
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
	result, err := json.Marshal(struct {
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
		return "", fmt.Errorf("encode Rust SDK resolution result: %w", err)
	}
	return string(result), nil
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
		cases = append([]string(nil), engineIntegrationCases...)
	}
	allowed := make(map[string]struct{}, len(engineIntegrationCases))
	for _, name := range engineIntegrationCases {
		allowed[name] = struct{}{}
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

	service := content.focusedService("rust-sdk-engine-integration")
	installed := content.integrationRunner(service).
		WithExec([]string{"dagger", "-y", "sdk", "install", "--here", "rust"})
	type caseResult struct {
		identity string
		err      error
	}
	results := make([]caseResult, len(cases))
	slots := make(chan struct{}, maxEngineIntegrationConcurrency)
	var group sync.WaitGroup
	for index, name := range cases {
		group.Add(1)
		go func() {
			defer group.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			var identity string
			var err error
			if name == "resolution" {
				identity, err = content.runResolution(ctx, service)
			} else {
				runner := installed.WithEnvVariable("RUST_SDK_ENGINE_INTEGRATION_CASE", name)
				identity, err = content.runEngineIntegrationCase(ctx, runner, name)
			}
			if err != nil {
				results[index].err = fmt.Errorf("run Rust engine-integration case %s: %w", name, err)
				return
			}
			results[index].identity = stableCaseObservation(name, identity)
		}()
	}
	group.Wait()

	observations := make(map[string]string, len(cases))
	for index, name := range cases {
		if results[index].err != nil {
			return "", results[index].err
		}
		observations[name] = results[index].identity
	}
	result, err := json.Marshal(struct {
		Cases            map[string]string `json:"cases"`
		DescriptorDigest string            `json:"descriptor_digest"`
		ManifestDigest   string            `json:"manifest_digest"`
	}{
		Cases: observations, DescriptorDigest: content.DescriptorDigest,
		ManifestDigest: content.ManifestDigest,
	})
	if err != nil {
		return "", fmt.Errorf("encode Rust engine-integration result: %w", err)
	}
	return string(result), nil
}

func (content *RustEngineContent) integrationRunner(
	service *dagger.Service,
) *dagger.Container {
	base := dag.Container().
		From(goHelperImage+"@"+goHelperDigest).
		WithDirectory("/work", dag.Directory()).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"}).
		WithExec([]string{"git", "config", "user.name", "Rust SDK Check"}).
		WithExec([]string{"git", "config", "user.email", "rust-sdk-check@dagger.invalid"}).
		WithExec([]string{"git", "commit", "--allow-empty", "-m", "initialize workspace"})
	return content.Engine.InstallClient(base, dagger.DaggerEngineInstallClientOpts{
		Service: service, Version: coreTargetVersion,
	})
}

func (content *RustEngineContent) runEngineIntegrationCase(
	ctx context.Context,
	runner *dagger.Container,
	name string,
) (string, error) {
	installed := runner
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
		fixture, err := enginefixture.NewOperationsPlan(workspaceConfig, coreTargetVersion)
		if err != nil {
			return "", fmt.Errorf("plan operations fixture: %w", err)
		}
		for _, file := range fixture.Files {
			result = result.WithNewFile(file.Path, file.Contents)
		}
		result = result.WithExec(fixture.GenerateClientArgs)
		if err := requirePaths(ctx, result.Directory("/work"), fixture.RequiredPaths); err != nil {
			return "", err
		}
		return collectOperationCaseResult(ctx, result)
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
		if err := requireProbeCalls(ctx, result, ".dagger/modules/runtime-checked", 2); err != nil {
			return "", fmt.Errorf("invoke overlapping checked Rust protocol calls: %w", err)
		}
		return result.Directory("/work/.dagger/modules/runtime-checked").Digest(ctx)
	case "runtime-legacy":
		result := installed.
			WithExec([]string{
				"dagger", "-y", "module", "init", "rust", "runtime-legacy", "--path", "modules/runtime-legacy", "--no-generate",
			}).
			WithExec([]string{"rm", "modules/runtime-legacy/dagger-module.toml"}).
			WithNewFile("/work/modules/runtime-legacy/dagger.json", `{"name":"runtime-legacy","engineVersion":"v1.0.0-beta.11.rust.1","sdk":{"source":"rust"}}`)
		functions, err := result.WithExec([]string{
			"dagger", "-m", "modules/runtime-legacy", "functions",
		}).Stdout(ctx)
		if err != nil {
			return "", fmt.Errorf("load legacy Rust runtime: %w", err)
		}
		if !strings.Contains(functions, "probe") {
			return "", fmt.Errorf("legacy Rust runtime omitted the fixed protocol surface")
		}
		if err := requireProbeCalls(ctx, result, "modules/runtime-legacy", 1); err != nil {
			return "", fmt.Errorf("invoke legacy Rust protocol call: %w", err)
		}
		generatedOnHost, err := result.Directory("/work").Exists(ctx, "modules/runtime-legacy/.dagger/rust/operation-manifest.json")
		if err != nil {
			return "", fmt.Errorf("inspect legacy host source: %w", err)
		}
		if generatedOnHost {
			return "", fmt.Errorf("legacy runtime generation escaped its private snapshot")
		}
		return result.Directory("/work/modules/runtime-legacy").Digest(ctx)
	case "negative-generated-lock-toolchain":
		return content.runGeneratedLockToolchainFailures(ctx, installed)
	case "negative-path-ownership":
		return content.runPathOwnershipFailures(ctx, installed)
	case "negative-redaction":
		return content.runRedactionFailure(ctx, installed)
	default:
		return "", fmt.Errorf("unreachable Rust engine-integration case %q", name)
	}
}

func (content *RustEngineContent) runGeneratedLockToolchainFailures(
	ctx context.Context,
	installed *dagger.Container,
) (string, error) {
	missing := installed.WithExec([]string{
		"dagger", "-y", "module", "init", "rust", "missing-generated", "--path", "modules/missing-generated", "--no-generate",
	})
	if err := requireRejectedExec(ctx, missing, []string{
		"dagger", "-m", "modules/missing-generated", "functions",
	}, ""); err != nil {
		return "", fmt.Errorf("missing committed generation boundary: %w", err)
	}

	stale := installed.WithExec([]string{
		"dagger", "-y", "module", "init", "rust", "stale-lock", "--path", "modules/stale-lock",
	})
	manifest, err := stale.File("/work/modules/stale-lock/Cargo.toml").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("read stale-lock fixture manifest: %w", err)
	}
	stale = stale.WithNewFile(
		"/work/modules/stale-lock/Cargo.toml",
		manifest+"\nanyhow = \"1.0.99\"\n",
	)
	if err := requireRejectedExec(ctx, stale, []string{
		"dagger", "-m", "modules/stale-lock", "functions",
	}, ""); err != nil {
		return "", fmt.Errorf("stale lockfile boundary: %w", err)
	}

	toolchain := installed.WithExec([]string{
		"dagger", "-y", "module", "init", "rust", "wrong-toolchain", "--path", "modules/wrong-toolchain",
	}).WithNewFile(
		"/work/modules/wrong-toolchain/rust-toolchain.toml",
		"[toolchain]\nchannel = \"1.96.0\"\nprofile = \"minimal\"\n",
	)
	if err := requireRejectedExec(ctx, toolchain, []string{
		"dagger", "-m", "modules/wrong-toolchain", "functions",
	}, ""); err != nil {
		return "", fmt.Errorf("incompatible toolchain boundary: %w", err)
	}
	return "missing-generation,stale-lockfile,incompatible-toolchain", nil
}

func (content *RustEngineContent) runPathOwnershipFailures(
	ctx context.Context,
	installed *dagger.Container,
) (string, error) {
	lexical := installed.WithExec(
		[]string{
			"dagger", "-y", "module", "init", "rust", "escape", "--path", "../../rust-sdk-escape",
		},
		dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny},
	)
	lexicalCode, err := lexical.ExitCode(ctx)
	if err != nil {
		return "", fmt.Errorf("inspect lexical escape rejection: %w", err)
	}
	if lexicalCode == 0 {
		return "", fmt.Errorf("lexical output escape unexpectedly succeeded")
	}

	symlink := installed.WithExec([]string{"mkdir", "-p", "/tmp/rust-sdk-outside", "/work/modules"}).
		WithExec([]string{"ln", "-s", "/tmp/rust-sdk-outside", "/work/modules/symlink"})
	if err := requireRejectedExec(ctx, symlink, []string{
		"dagger", "-y", "module", "init", "rust", "symlink", "--path", "modules/symlink",
	}, ""); err != nil {
		return "", fmt.Errorf("symlink output escape boundary: %w", err)
	}

	const callerEntrypoint = "fn caller_owned() {}\n"
	collision := installed.
		WithNewFile("/work/modules/collision/Cargo.toml", "[package]\nname = \"collision\"\nversion = \"0.1.0\"\nedition = \"2024\"\n").
		WithNewFile("/work/modules/collision/src/bin/dagger-module.rs", callerEntrypoint)
	if err := requireRejectedExec(ctx, collision, []string{
		"dagger", "-y", "module", "init", "rust", "collision", "--path", "modules/collision",
	}, ""); err != nil {
		return "", fmt.Errorf("unknown ownership collision boundary: %w", err)
	}
	contents, err := collision.File("/work/modules/collision/src/bin/dagger-module.rs").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("read caller-owned collision fixture: %w", err)
	}
	if contents != callerEntrypoint {
		return "", fmt.Errorf("rejected ownership collision changed caller-owned content")
	}
	return "lexical-escape,symlink-escape,ownership-collision", nil
}

func (content *RustEngineContent) runRedactionFailure(
	ctx context.Context,
	installed *dagger.Container,
) (string, error) {
	const credential = "rust-sdk-secret-should-not-render"
	manifest := `[package]
name = "credential-failure"
version = "0.1.0"
edition = "2024"
rust-version = "1.97.1"

[dependencies]
dagger-sdk = { git = "https://user:` + credential + `@github.com/iw/dagger", rev = "501b57e0476dee5881b99a064c3c04173134ecc7" }
`
	fixture := installed.
		WithNewFile("/work/modules/credential-failure/Cargo.toml", manifest).
		WithNewFile("/work/modules/credential-failure/src/lib.rs", "pub fn caller_owned() {}\n")
	if err := requireRejectedExec(ctx, fixture, []string{
		"dagger", "-y", "module", "init", "rust", "credential-failure", "--path", "modules/credential-failure",
	}, credential); err != nil {
		return "", fmt.Errorf("credential-bearing dependency boundary: %w", err)
	}
	return "credential-redacted", nil
}

func requireProbeCalls(
	ctx context.Context,
	runner *dagger.Container,
	module string,
	count int,
) error {
	results := make([]struct {
		output string
		err    error
	}, count)
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index].output, results[index].err = runner.
				WithEnvVariable("RUST_SDK_PROTOCOL_CALL", fmt.Sprintf("call-%d", index)).
				WithExec([]string{"dagger", "-m", module, "call", "probe"}).
				Stdout(ctx)
		}()
	}
	group.Wait()
	for index, result := range results {
		if result.err != nil {
			return fmt.Errorf("protocol call %d: %w", index, result.err)
		}
		if strings.TrimSpace(result.output) != "rust-sdk-protocol-ok" {
			return fmt.Errorf("protocol call %d returned an unexpected scalar", index)
		}
	}
	return nil
}

func requireRejectedExec(
	ctx context.Context,
	runner *dagger.Container,
	args []string,
	secret string,
) error {
	rejected := runner.WithExec(args, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	exitCode, err := rejected.ExitCode(ctx)
	if err != nil {
		return fmt.Errorf("inspect rejected command: %w", err)
	}
	if exitCode == 0 {
		return fmt.Errorf("boundary command unexpectedly succeeded")
	}
	stderr, err := rejected.Stderr(ctx)
	if err != nil {
		return fmt.Errorf("read rejected command diagnostic: %w", err)
	}
	if strings.TrimSpace(stderr) == "" {
		return fmt.Errorf("boundary command failed without an actionable diagnostic")
	}
	if secret != "" && strings.Contains(stderr, secret) {
		return fmt.Errorf("boundary diagnostic exposed the injected credential")
	}
	return nil
}

func stableCaseObservation(name, identity string) string {
	digest := sha256.Sum256([]byte("rust-sdk-engine-case-v1\x00" + name + "\x00" + identity))
	return fmt.Sprintf("sha256:%x", digest)
}

func collectOperationCaseResult(
	ctx context.Context,
	result *dagger.Container,
) (string, error) {
	root := result.Directory("/work")
	observation, err := root.Digest(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve operation workspace identity: %w", err)
	}
	paths, err := root.Glob(ctx, "**/.dagger/rust/operation-manifest.json")
	if err != nil {
		return "", fmt.Errorf("enumerate operation manifests: %w", err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("operation selectors produced no ownership manifest")
	}
	for _, path := range paths {
		contents, err := root.File(path).Contents(ctx)
		if err != nil {
			return "", fmt.Errorf("read operation manifest %s: %w", path, err)
		}
		var manifest struct {
			InputDigest string `json:"input_digest"`
		}
		if err := json.Unmarshal([]byte(contents), &manifest); err != nil {
			return "", fmt.Errorf("decode operation manifest %s: %w", path, err)
		}
		if !isCanonicalSHA256(manifest.InputDigest) {
			return "", fmt.Errorf("operation manifest %s omitted its canonical input identity", path)
		}
	}
	return observation, nil
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
			cargoManifests, globErr := root.Glob(ctx, "**/Cargo.toml")
			if globErr != nil {
				return fmt.Errorf("required path %s is absent; inspect Cargo manifests: %w", candidate, globErr)
			}
			return fmt.Errorf(
				"required path %s is absent; operation manifests: %v; Cargo manifests: %v",
				candidate,
				manifests,
				cargoManifests,
			)
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
func (t *RustClientDev) APIClient() *dagger.Changeset {
	return t.WithGeneratedClient().Changes()
}

func (t *RustClientDev) Changes() *dagger.Changeset {
	return t.Workspace.Changes(t.OriginalWorkspace)
}

func (t *RustClientDev) WithGeneratedClient() *RustClientDev {
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
func (t *RustClientDev) GeneratedClientCheck(ctx context.Context) error {
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
		WithEnvVariable("RUSTDOCFLAGS", "-D warnings").
		WithExec([]string{
			"cargo", "doc", "-p", "dagger-sdk", "--all-features", "--no-deps", "--locked",
		}).
		Sync(ctx)

	return err
}

// Exercise representative generated Core operations against the exact target engine.
//
// +check
func (t *RustClientDev) GeneratedCoreIntegration(ctx context.Context) error {
	generated := *t
	generated.WithGeneratedClient()
	generated.Workspace = generated.Workspace.WithFile(
		generated.SourcePath+"/crates/dagger-sdk/examples/core_conformance.rs",
		t.Ws.File(".dagger/modules/rust-client-dev/testdata/core_conformance.rs"),
	)

	exactEngine := t.coreTargetEngine()
	service := exactEngine.Service("rust-sdk-core-conformance", dagger.DaggerEngineServiceOpts{
		Version: coreTargetVersion,
	})
	runner := exactEngine.InstallClient(generated.devContainer(rustBaseContainer(), true), dagger.DaggerEngineInstallClientOpts{
		Service: service,
		Version: coreTargetVersion,
	})
	_, err := runner.
		WithExec([]string{
			"dagger", "run", "cargo", "run", "-p", "dagger-sdk", "--example", "core_conformance", "--locked",
		}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("run exact-target generated Core integration: %w", err)
	}
	return nil
}

func (t *RustClientDev) coreTargetEngine() *dagger.DaggerEngine {
	// Keep the immutable Core contract and revision while giving this fork's engine and
	// CLI binaries the release version that the Rust client validates at connection time.
	source := dag.Git(coreTargetRepository).
		Commit(coreTargetRevision).
		Tree(dagger.GitCommitTreeOpts{DiscardGitDir: true}).
		WithNewFile("internal/version/VERSION", coreTargetVersion+"\n")
	return dag.DaggerEngine(dagger.DaggerEngineOpts{
		ClientDockerConfig: t.ClientDockerConfig,
		Ws:                 t.Ws,
	}).WithGitSource(coreTargetRepository, coreTargetRevision).WithSource(source)
}

// RustSdkBuild is the ordinary, non-publishing Rust SDK build result.
type RustSdkBuild struct {
	// Packages contains exactly dagger-sdk-macros and dagger-sdk as .crate files.
	// An authorized operator may export these files for a manually invoked GitHub Release;
	// this build object has no publication operation.
	Packages *dagger.Directory

	// CompleteEngine contains the standard engine binaries and all standard SDK content,
	// with the Rust content produced from the current workspace.
	CompleteEngine *dagger.Container

	Version string

	Checker      *dagger.File         // +private
	TargetEngine *dagger.DaggerEngine // +private
	NetworkCIDR  string               // +private
	Consumer     *dagger.Directory    // +private
}

// Build creates the two public Rust packages and the ordinary complete engine.
// It validates both outputs and performs no publication or external mutation.
func (t *RustClientDev) Build(
	ctx context.Context,
	// +optional
	platform dagger.Platform,
) (*RustSdkBuild, error) {
	version, err := t.workspacePackageVersion(ctx)
	if err != nil {
		return nil, err
	}

	packageContainer := t.DevContainer(true).
		WithExec([]string{
			"cargo", "build", "-p", "dagger-bootstrap", "--bin", "dagger-rust-sdk-check", "--locked",
		}).
		WithExec([]string{
			"cargo", "package", "-p", rustSdkMacrosCrate, "--locked",
		}).
		WithExec([]string{
			"cargo", "package", "-p", rustSdkCrate, "--all-features", "--locked",
			"--config", `patch.crates-io.dagger-sdk-macros.path="crates/dagger-sdk-macros"`,
		}).
		WithExec([]string{
			"/src/sdk/rust/target/debug/dagger-rust-sdk-check", "packages",
			"--workspace", "/src/sdk/rust",
			"--packages", "/src/sdk/rust/target/package",
		})
	if _, err := packageContainer.Sync(ctx); err != nil {
		return nil, fmt.Errorf("build and validate public Rust packages: %w", err)
	}
	checker := packageContainer.File("/src/sdk/rust/target/debug/dagger-rust-sdk-check")
	packages := packageContainer.
		Directory("/src/sdk/rust/target/package").
		Filter(dagger.DirectoryFilterOpts{Include: []string{"*.crate"}})

	content, err := t.EngineContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("build reusable Rust engine content: %w", err)
	}
	target := t.coreTargetEngine()
	completeEngine := target.ContainerWithRustSdkcontent(
		content.Built,
		dagger.DaggerEngineContainerWithRustSdkcontentOpts{
			Platform: platform,
			Version:  coreTargetVersion,
		},
	)
	selectedManifest, err := completeEngine.EnvVariable(ctx, "DAGGER_RUST_SDK_MANIFEST_DIGEST")
	if err != nil {
		return nil, fmt.Errorf("read complete engine Rust manifest: %w", err)
	}
	for _, path := range []string{"/usr/local/bin/dagger-engine", "/usr/local/bin/dagger"} {
		if _, err := completeEngine.File(path).Sync(ctx); err != nil {
			return nil, fmt.Errorf("build complete engine file %s: %w", path, err)
		}
	}
	engineCheck := rustBaseContainer().
		WithFile("/usr/local/bin/dagger-rust-sdk-check", checker).
		WithDirectory(
			"/content",
			completeEngine.Directory("/usr/local/share/dagger/content"),
		).
		WithExec([]string{
			"dagger-rust-sdk-check", "engine",
			"--content", "/content",
			"--expected-rust-manifest", content.ManifestDigest,
			"--rust-manifest", selectedManifest,
		})
	if _, err := engineCheck.Sync(ctx); err != nil {
		return nil, fmt.Errorf("validate complete engine Rust content: %w", err)
	}
	networkCIDR, err := target.NetworkCidr(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve complete engine network: %w", err)
	}

	return &RustSdkBuild{
		Packages:       packages,
		CompleteEngine: completeEngine,
		Version:        version,
		Checker:        checker,
		TargetEngine:   target,
		NetworkCIDR:    networkCIDR,
		Consumer: t.Ws.Directory(
			".dagger/modules/rust-client-dev/testdata/external-consumer",
		),
	}, nil
}

func (t *RustClientDev) workspacePackageVersion(ctx context.Context) (string, error) {
	contents, err := t.Ws.File(t.SourcePath + "/Cargo.toml").Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("read Rust workspace manifest: %w", err)
	}
	var manifest struct {
		Workspace struct {
			Package struct {
				Version string `toml:"version"`
			} `toml:"package"`
		} `toml:"workspace"`
	}
	if _, err := toml.Decode(contents, &manifest); err != nil {
		return "", fmt.Errorf("decode Rust workspace manifest: %w", err)
	}
	version := strings.TrimSpace(manifest.Workspace.Package.Version)
	if version == "" {
		return "", fmt.Errorf("Rust workspace package version is empty")
	}
	return version, nil
}

// Verify compiles one isolated consumer from the packaged crates, queries this build's
// complete engine, and closes the Rust SDK client cleanly.
//
// +check
func (build *RustSdkBuild) Verify(ctx context.Context) error {
	if build.Packages == nil || build.CompleteEngine == nil || build.Checker == nil ||
		build.TargetEngine == nil || build.Consumer == nil || build.NetworkCIDR == "" {
		return fmt.Errorf("ordinary Rust SDK build is incomplete")
	}

	unpacked := rustBaseContainer().
		WithFile("/usr/local/bin/dagger-rust-sdk-check", build.Checker).
		WithDirectory("/work/packages", build.Packages).
		WithDirectory("/work/vendor", dag.Directory()).
		WithExec([]string{
			"dagger-rust-sdk-check", "unpack",
			"--packages", "/work/packages",
			"--destination", "/work/vendor",
		})
	vendor := unpacked.Directory("/work/vendor")

	service := build.CompleteEngine.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{
			Protocol: dagger.NetworkProtocolTcp,
		}).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"--addr", "tcp://0.0.0.0:1234",
				"--network-name", "dagger-dev",
				"--network-cidr", build.NetworkCIDR,
			},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})
	runner := build.TargetEngine.InstallClient(
		rustBaseContainer().
			WithDirectory("/work/consumer", build.Consumer).
			WithDirectory("/work/vendor", vendor).
			WithWorkdir("/work/consumer"),
		dagger.DaggerEngineInstallClientOpts{
			Service: service,
			Version: coreTargetVersion,
		},
	)
	if _, err := runner.
		WithExec([]string{"dagger", "run", "cargo", "run", "--locked"}).
		Sync(ctx); err != nil {
		return fmt.Errorf("verify external Rust consumer: %w", err)
	}
	return nil
}
