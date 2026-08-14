// Toolchain to develop and verify the Dagger Rust SDK.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"dagger/rust-sdk-dev/internal/enginefixture"
	signoffmodel "dagger/rust-sdk-dev/internal/signoff"
	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"

	"dagger/rust-sdk-dev/internal/dagger"
)

const (
	rustSdkImage            = "rust:1.97.1-bookworm"
	rustToolchainVersion    = "1.97.1"
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

type sdkDependencyEvidence struct {
	Source       string `json:"source"`
	Registry     string `json:"registry,omitempty"`
	Package      string `json:"package"`
	ExactVersion string `json:"exact_version,omitempty"`
	URL          string `json:"url,omitempty"`
	Revision     string `json:"revision,omitempty"`
}

type operationCaseEvidence struct {
	Observation              string   `json:"observation"`
	OperationInputDigests    []string `json:"operation_input_digests"`
	OperationManifestDigests []string `json:"operation_manifest_digests"`
}

// Develop and verify the Dagger Rust SDK.
type RustSdkDev struct {
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
	// Full reachable revision in the engine repository containing the public dagger-sdk package.
	// +optional
	sdkDependencyRevision string,
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
		OriginalWorkspace:     rustSrc,
		Workspace:             rustSrc,
		SourcePath:            sourcePath,
		BaseContainer:         rustBaseContainer(),
		Ws:                    workspace,
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

// focusedImmutableEngineSource selects the same subject-owned closure directly from an
// immutable Git tree. Sign-off must not reuse focusedEngineSource: that helper intentionally
// follows the live development workspace and would reopen a checkout-to-build TOCTOU window.
func focusedImmutableEngineSource(source *dagger.Directory) *dagger.Directory {
	return source.Filter(dagger.DirectoryFilterOpts{Include: []string{
		"LICENSE",
		"go.mod",
		"go.sum",
		"analytics/**",
		"auth/**",
		"cmd/**",
		"core/**",
		"dagql/**",
		"engine/**",
		"internal/**",
		"network/**",
		"util/**",
		"sdk/go/**",
		"sdk/rust/Cargo.lock",
		"sdk/rust/Cargo.toml",
		"sdk/rust/rust-toolchain.toml",
		"sdk/rust/completeness/target.json",
		"sdk/rust/completeness/snapshots/schema.json",
		"sdk/rust/crates/**/Cargo.toml",
		"sdk/rust/crates/**/src/**",
		"sdk/rust/crates/**/assets/**",
		"sdk/rust/runtime/**",
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
		// owns only SDK resolution and its deliberately bounded initializer surface, so keep
		// it deterministic and offline by selecting those production boundaries directly.
		WithExec([]string{
			"go", "test", "./internal/cmd/dagger", "-run",
			"^(TestSDKResolveInstall|TestPackagedRustSDKRegistersOnlyImplementedInitializer)$",
		}).
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
	Content                    *dagger.Directory
	ManifestDigest             string
	DescriptorDigest           string
	dependencyDescriptor       string
	dependencyDescriptorDigest string
	MappingDigest              string
	CompletenessTargetDigest   string
	SDKDependency              sdkDependencyEvidence                 // +private
	Engine                     *dagger.DaggerEngine                  // +private
	Built                      *dagger.DaggerEngineRustEngineContent // +private
}

// EngineContent builds the Rust SDK content once and returns its reusable graph object.
func (t *RustSdkDev) EngineContent(ctx context.Context) (*RustEngineContent, error) {
	engine := dag.DaggerEngine(dagger.DaggerEngineOpts{
		ClientDockerConfig: t.ClientDockerConfig,
		Ws:                 t.Ws,
		VcsRepository:      t.EngineRepository,
	}).WithSource(t.focusedEngineSource())
	dependencyRepository := ""
	dependencyRevision := ""
	if t.SDKDependencyRevision != "" {
		dependencyRepository = t.EngineRepository
		dependencyRevision = t.SDKDependencyRevision
	}
	return t.engineContent(ctx, engine, t.Ws.Directory("/"), dependencyRepository, dependencyRevision)
}

// signoffEngineContent constructs every subject-owned component and evidence input from the same
// full immutable Git coordinate admitted before graph construction. WithGitSource also gives
// nested engine toolchains a workspace derived from that ref, so no ambient checkout can leak
// through a module constructor after the focused source filter is applied.
func (t *RustSdkDev) signoffEngineContent(
	ctx context.Context,
	subject signoffmodel.ArtifactSubject,
) (*RustEngineContent, error) {
	ref := dag.Git(subject.Repository).Commit(subject.Revision)
	immutableWorkspace := ref.AsWorkspace()
	immutableTree := ref.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	engine := dag.DaggerEngine(dagger.DaggerEngineOpts{
		ClientDockerConfig: t.ClientDockerConfig,
		VcsRepository:      subject.Repository,
		Ws:                 immutableWorkspace,
	}).WithGitSource(subject.Repository, subject.Revision).
		WithSource(focusedImmutableEngineSource(immutableTree))
	return t.engineContent(ctx, engine, immutableTree, subject.Repository, subject.Revision)
}

func (t *RustSdkDev) engineContent(
	ctx context.Context,
	engine *dagger.DaggerEngine,
	evidenceRoot *dagger.Directory,
	dependencyRepository string,
	dependencyRevision string,
) (*RustEngineContent, error) {
	contentOptions := dagger.DaggerEngineRustSdkcontentOpts{Version: coreTargetVersion}
	if dependencyRevision != "" {
		contentOptions.DependencyRepository = dependencyRepository
		contentOptions.DependencyRevision = dependencyRevision
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
	var sdkDependency sdkDependencyEvidence
	if err := json.Unmarshal([]byte(dependencyDescriptor), &sdkDependency); err != nil {
		return nil, fmt.Errorf("decode Rust SDK dependency descriptor: %w", err)
	}
	if sdkDependency.Package != rustSdkCrate ||
		(sdkDependency.Source != "registry" && sdkDependency.Source != "git") {
		return nil, fmt.Errorf("Rust SDK content returned an unsupported dependency descriptor")
	}
	canonicalDependencyDescriptor, err := json.Marshal(sdkDependency)
	if err != nil || string(canonicalDependencyDescriptor) != dependencyDescriptor {
		return nil, fmt.Errorf("Rust SDK content returned a non-canonical dependency descriptor")
	}
	if dependencyRevision != "" {
		expectedRepository := strings.TrimSuffix(dependencyRepository, ".git")
		if strings.HasPrefix(expectedRepository, "github.com/") {
			expectedRepository = "https://" + expectedRepository
		}
		if sdkDependency.Source != "git" || sdkDependency.Registry != "" ||
			sdkDependency.ExactVersion != "" || sdkDependency.URL != expectedRepository ||
			sdkDependency.Revision != dependencyRevision {
			return nil, fmt.Errorf("Rust SDK content did not retain the exact fork Git dependency")
		}
	}
	dependencyDescriptorIdentity := sha256.Sum256(canonicalDependencyDescriptor)
	mappingContents, err := evidenceRoot.File("sdk/rust/completeness/engine-integration-mappings.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Rust engine-integration mappings: %w", err)
	}
	var mappingSubject struct {
		TargetDigest string `json:"target_digest"`
	}
	if err := json.Unmarshal([]byte(mappingContents), &mappingSubject); err != nil {
		return nil, fmt.Errorf("decode Rust engine-integration mapping subject: %w", err)
	}
	if !isCanonicalSHA256(mappingSubject.TargetDigest) {
		return nil, fmt.Errorf("Rust engine-integration mappings returned a malformed target identity")
	}
	mappingDigest := sha256.Sum256([]byte(mappingContents))
	return &RustEngineContent{
		Content:                    built.Content(),
		ManifestDigest:             manifestDigest,
		DescriptorDigest:           descriptorDigest,
		dependencyDescriptor:       dependencyDescriptor,
		dependencyDescriptorDigest: fmt.Sprintf("sha256:%x", dependencyDescriptorIdentity),
		MappingDigest:              fmt.Sprintf("sha256:%x", mappingDigest),
		CompletenessTargetDigest:   mappingSubject.TargetDigest,
		SDKDependency:              sdkDependency,
		Engine:                     engine,
		Built:                      built,
	}, nil
}

// Resolution starts an exact-target engine from this object's content and exercises
// the built-in loader and workspace install path without reconstructing that content.
func (content *RustEngineContent) Resolution(ctx context.Context) (string, error) {
	if content.Engine == nil || content.Built == nil {
		return "", fmt.Errorf("Rust SDK content is detached from its engine construction graph")
	}
	service := content.focusedService("rust-sdk-resolution")
	return content.runResolution(ctx, content.installedBaseline(service))
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
	installed *dagger.Container,
) (string, error) {
	if err := verifyInstalledRustResolution(ctx, installed); err != nil {
		return "", err
	}
	evidence, err := json.Marshal(struct {
		DescriptorDigest  string `json:"descriptor_digest"`
		Installed         bool   `json:"installed"`
		ManifestDigest    string `json:"manifest_digest"`
		SingleInstall     bool   `json:"single_install"`
		ShorthandRejected bool   `json:"shorthand_rejected"`
	}{
		DescriptorDigest:  content.DescriptorDigest,
		Installed:         true,
		ManifestDigest:    content.ManifestDigest,
		SingleInstall:     true,
		ShorthandRejected: true,
	})
	if err != nil {
		return "", fmt.Errorf("encode Rust SDK resolution evidence: %w", err)
	}
	return string(evidence), nil
}

// verifyInstalledRustResolution owns the loader assertions shared by focused development and
// exact-target sign-off. Keeping one assertion path prevents the sign-off adapter from silently
// weakening the already-reviewed resolution boundary into a CLI reachability check.
func verifyInstalledRustResolution(ctx context.Context, installed *dagger.Container) error {
	installedConfig, err := installed.File("/work/dagger.toml").Contents(ctx)
	if err != nil {
		return fmt.Errorf("install bare Rust SDK: %w", err)
	}
	if !strings.Contains(installedConfig, "dagger-rust-sdk") {
		return fmt.Errorf("installed workspace configuration omitted the Rust SDK")
	}
	installedList, err := installed.WithExec([]string{"dagger", "sdk", "installed"}).Stdout(ctx)
	if err != nil {
		return fmt.Errorf("list installed Rust SDK: %w", err)
	}
	if !strings.Contains(installedList, "rust") {
		return fmt.Errorf("installed SDK listing omitted canonical Rust entry")
	}
	rejected := installed.WithExec(
		[]string{"dagger", "-y", "sdk", "install", "rust@v1.0.0-beta.10"},
		dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny},
	)
	exitCode, err := rejected.ExitCode(ctx)
	if err != nil {
		return fmt.Errorf("inspect Rust shorthand rejection: %w", err)
	}
	if exitCode == 0 {
		return fmt.Errorf("versioned Rust built-in unexpectedly reached external resolution")
	}
	stderr, err := rejected.Stderr(ctx)
	if err != nil {
		return fmt.Errorf("read Rust shorthand rejection: %w", err)
	}
	if !strings.Contains(stderr, "does not currently support selecting a specific version") {
		return fmt.Errorf("versioned Rust built-in failed without the stable pre-fallback diagnostic")
	}
	return nil
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
	installed := content.installedBaseline(service)
	type caseResult struct {
		identity                 string
		operationInputDigests    []string
		operationManifestDigests []string
		err                      error
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
				identity, err = content.runResolution(ctx, installed)
			} else {
				runner := installed.WithEnvVariable("RUST_SDK_ENGINE_INTEGRATION_CASE", name)
				identity, err = runEngineIntegrationCase(ctx, runner, name)
			}
			if err != nil {
				results[index].err = fmt.Errorf("run Rust engine-integration case %s: %w", name, err)
				return
			}
			if name == "operations" {
				var operationEvidence operationCaseEvidence
				if err := json.Unmarshal([]byte(identity), &operationEvidence); err != nil {
					results[index].err = fmt.Errorf("decode operation evidence: %w", err)
					return
				}
				identity = operationEvidence.Observation
				results[index].operationInputDigests = operationEvidence.OperationInputDigests
				results[index].operationManifestDigests = operationEvidence.OperationManifestDigests
			}
			results[index].identity = stableCaseObservation(name, identity)
		}()
	}
	group.Wait()

	observations := make(map[string]string, len(cases))
	operationInputDigests := map[string]struct{}{}
	operationManifestDigests := map[string]struct{}{}
	for index, name := range cases {
		if results[index].err != nil {
			return "", results[index].err
		}
		observations[name] = results[index].identity
		for _, digest := range results[index].operationInputDigests {
			operationInputDigests[digest] = struct{}{}
		}
		for _, digest := range results[index].operationManifestDigests {
			operationManifestDigests[digest] = struct{}{}
		}
	}
	inputDigests := sortedDigestSet(operationInputDigests)
	manifestDigests := sortedDigestSet(operationManifestDigests)
	evidence, err := json.Marshal(struct {
		Cases                    map[string]string `json:"cases"`
		DescriptorDigest         string            `json:"descriptor_digest"`
		ManifestDigest           string            `json:"manifest_digest"`
		OperationInputDigests    []string          `json:"operation_input_digests"`
		OperationManifestDigests []string          `json:"operation_manifest_digests"`
	}{
		Cases: observations, DescriptorDigest: content.DescriptorDigest,
		ManifestDigest: content.ManifestDigest, OperationInputDigests: inputDigests,
		OperationManifestDigests: manifestDigests,
	})
	if err != nil {
		return "", fmt.Errorf("encode Rust engine-integration evidence: %w", err)
	}
	return string(evidence), nil
}

// EngineEvidence runs the complete closed case set before publishing target-bound evidence.
//
// A caller cannot supply selectors here: focused case subsets are useful during development but
// are never equivalent to the complete matrix admitted by the completeness contract.
func (content *RustEngineContent) EngineEvidence(ctx context.Context) (string, error) {
	integration, err := content.EngineIntegration(ctx, append([]string(nil), engineIntegrationCases...))
	if err != nil {
		return "", err
	}
	var result struct {
		Cases                    map[string]string `json:"cases"`
		DescriptorDigest         string            `json:"descriptor_digest"`
		ManifestDigest           string            `json:"manifest_digest"`
		OperationInputDigests    []string          `json:"operation_input_digests"`
		OperationManifestDigests []string          `json:"operation_manifest_digests"`
	}
	if err := json.Unmarshal([]byte(integration), &result); err != nil {
		return "", fmt.Errorf("decode complete Rust engine-integration result: %w", err)
	}
	if err := requireCompleteEngineCaseSet(result.Cases); err != nil {
		return "", err
	}
	if len(result.OperationInputDigests) == 0 || len(result.OperationManifestDigests) == 0 {
		return "", fmt.Errorf("engine evidence requires canonical operation provenance")
	}
	evidence, err := json.Marshal(struct {
		FormatVersion            int                   `json:"format_version"`
		Cases                    map[string]string     `json:"cases"`
		CompletenessTargetDigest string                `json:"completeness_target_digest"`
		DescriptorDigest         string                `json:"descriptor_digest"`
		EngineRevision           string                `json:"engine_revision"`
		EngineVersion            string                `json:"engine_version"`
		ManifestDigest           string                `json:"manifest_digest"`
		MappingDigest            string                `json:"mapping_digest"`
		OperationInputDigests    []string              `json:"operation_input_digests"`
		OperationManifestDigests []string              `json:"operation_manifest_digests"`
		RustToolchain            string                `json:"rust_toolchain"`
		SDKDependency            sdkDependencyEvidence `json:"sdk_dependency"`
	}{
		FormatVersion:            1,
		Cases:                    result.Cases,
		CompletenessTargetDigest: content.CompletenessTargetDigest,
		DescriptorDigest:         content.DescriptorDigest,
		EngineRevision:           coreTargetRevision,
		EngineVersion:            coreTargetVersion,
		ManifestDigest:           content.ManifestDigest,
		MappingDigest:            content.MappingDigest,
		OperationInputDigests:    result.OperationInputDigests,
		OperationManifestDigests: result.OperationManifestDigests,
		RustToolchain:            rustToolchainVersion,
		SDKDependency:            content.SDKDependency,
	})
	if err != nil {
		return "", fmt.Errorf("encode complete Rust engine evidence: %w", err)
	}
	return string(evidence), nil
}

func (content *RustEngineContent) installedBaseline(
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
	}).WithExec([]string{"dagger", "-y", "sdk", "install", "--here", "rust"})
}

func runEngineIntegrationCase(
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
		return collectOperationCaseEvidence(ctx, result)
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
		return runGeneratedLockToolchainFailures(ctx, installed)
	case "negative-path-ownership":
		return runPathOwnershipFailures(ctx, installed)
	case "negative-redaction":
		return runRedactionFailure(ctx, installed)
	default:
		return "", fmt.Errorf("unreachable Rust engine-integration case %q", name)
	}
}

func runGeneratedLockToolchainFailures(
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

func runPathOwnershipFailures(
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

func runRedactionFailure(
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
dagger-sdk = { git = "https://user:` + credential + `@github.com/iw/dagger", rev = "25300124ca110612edc09c43f89cb5fad6028170" }
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

func collectOperationCaseEvidence(
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
	inputDigests := map[string]struct{}{}
	manifestDigests := map[string]struct{}{}
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
		inputDigests[manifest.InputDigest] = struct{}{}
		digest := sha256.New()
		_, _ = digest.Write([]byte("dagger-rust-engine-operation-manifest-v1\x00"))
		_, _ = digest.Write([]byte(contents))
		manifestDigests[fmt.Sprintf("sha256:%x", digest.Sum(nil))] = struct{}{}
	}
	evidence, err := json.Marshal(operationCaseEvidence{
		Observation:              observation,
		OperationInputDigests:    sortedDigestSet(inputDigests),
		OperationManifestDigests: sortedDigestSet(manifestDigests),
	})
	if err != nil {
		return "", fmt.Errorf("encode operation identities: %w", err)
	}
	return string(evidence), nil
}

func sortedDigestSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func requireCompleteEngineCaseSet(observations map[string]string) error {
	if len(observations) != len(engineIntegrationCases) {
		return fmt.Errorf("engine evidence requires all %d named cases", len(engineIntegrationCases))
	}
	for _, name := range engineIntegrationCases {
		identity, found := observations[name]
		if !found || !isCanonicalSHA256(identity) {
			return fmt.Errorf("engine evidence is missing a canonical result for case %q", name)
		}
	}
	return nil
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
