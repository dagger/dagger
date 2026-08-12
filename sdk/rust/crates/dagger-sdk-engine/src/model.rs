//! Canonical operational, project, provenance, and packaged-asset models.
//!
//! These data-only types are the private contract shared by the Rust operation tool,
//! the thin engine adapter, and completeness evidence. They contain no ambient paths,
//! credentials, command strings, process handles, or Dagger objects. BTree collections
//! make semantically unordered data deterministic before canonical encoding.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Deserializer, Serialize, Serializer};

use dagger_codegen::client::{CargoPackageName, RustIdentifier};
pub use dagger_codegen::engine::OperationKind;

use crate::scalar::{
    CanonicalRegistry, CanonicalRepositoryUrl, ExactRustToolchain, ExactVersion, FormatVersion,
    FullRevision, RelativeOperationPath, RustTarget, SdkPackageName, Sha256Digest,
    StableCoordinate,
};

/// Immutable target coordinates which affect every engine operation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TargetIdentity {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Immutable engine repository origin.
    pub repository: CanonicalRepositoryUrl,
    /// Full Dagger source revision.
    pub dagger_revision: FullRevision,
    /// Exact engine release version.
    pub engine_version: ExactVersion,
    /// Exact public Rust SDK version.
    pub rust_sdk_version: ExactVersion,
    /// Exact Rust compiler toolchain.
    pub rust_toolchain: ExactRustToolchain,
    /// Canonical core-schema identity.
    pub core_schema_digest: Sha256Digest,
}

/// One schema document mounted at a fixed, operation-relative path.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SchemaInput {
    /// Path inside the explicit operation input root.
    pub path: RelativeOperationPath,
    /// Digest of the exact schema bytes.
    pub digest: Sha256Digest,
}

/// Module configuration generation mode selected by the target engine.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleConfigFormat {
    /// Current module configuration with committed generated files.
    Current,
    /// Legacy module configuration with private runtime generation.
    Legacy,
}

/// Complete module identity supplied to an operation that is scoped to module source.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleOperationInput {
    /// Normalized module name used by the engine.
    pub name: StableCoordinate,
    /// Original caller-visible module name retained for diagnostics and generation.
    pub original_name: StableCoordinate,
    /// Engine-selected source path beneath the operation root.
    pub source_subpath: RelativeOperationPath,
    /// Current or legacy configuration semantics.
    pub config_format: ModuleConfigFormat,
    /// Digest of the complete selected module source.
    pub source_digest: Sha256Digest,
    /// Exact resolved remote revision; absent for workspace-local mutable source.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub resolved_pin: Option<FullRevision>,
}

/// Credential-free identity of the one module selected for a standalone client.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientModuleIdentity {
    /// Engine-normalized name used by `Query.<module>`.
    pub name: StableCoordinate,
    /// Original display name retained for safe documentation and name planning.
    pub original_name: StableCoordinate,
    /// Engine-selected source subtree beneath the operation root.
    pub source_subpath: RelativeOperationPath,
    /// Digest of the complete selected authored module source.
    pub source_digest: Sha256Digest,
    /// Exact resolved remote revision; absent for workspace-local mutable source.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub resolved_pin: Option<FullRevision>,
}

impl From<&ModuleOperationInput> for ClientModuleIdentity {
    fn from(module: &ModuleOperationInput) -> Self {
        Self {
            name: module.name.clone(),
            original_name: module.original_name.clone(),
            source_subpath: module.source_subpath.clone(),
            source_digest: module.source_digest.clone(),
            resolved_pin: module.resolved_pin.clone(),
        }
    }
}

/// Deterministic Cargo and Rust identity selected for a standalone client project.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientProjectIdentity {
    /// Exact Cargo package spelling retained in the project manifest.
    pub package_name: CargoPackageName,
    /// Rust crate spelling used by generated examples and documentation.
    pub crate_name: RustIdentifier,
}

/// Immutable dependency descriptor for generated projects.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(tag = "source", rename_all = "kebab-case", deny_unknown_fields)]
pub enum PublishedSdkDependency {
    /// Exact public package from the approved Cargo registry.
    Registry {
        /// Canonical registry identity.
        registry: CanonicalRegistry,
        /// The sole public SDK package.
        package: SdkPackageName,
        /// Exact registry version, rendered with an equals requirement.
        exact_version: ExactVersion,
    },
    /// Public SDK package from an immutable repository revision.
    Git {
        /// Credential-free HTTPS repository URL.
        url: CanonicalRepositoryUrl,
        /// Full immutable Git commit.
        revision: FullRevision,
        /// The sole public SDK package.
        package: SdkPackageName,
    },
}

/// Complete semantic input to one engine operation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OperationRequest {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Closed operation selector.
    pub operation: OperationKind,
    /// Exact engine and Rust SDK target.
    pub target: TargetIdentity,
    /// Complete engine-visible schema identity.
    pub visible_schema: SchemaInput,
    /// Module identity when required by the selected operation.
    pub module: Option<ModuleOperationInput>,
    /// Immutable dependency emitted into generated Cargo manifests.
    pub sdk_dependency: PublishedSdkDependency,
    /// Engine-selected output subtree.
    pub output_root: RelativeOperationPath,
}

/// Complete semantic input to Rust-owned project initialization.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InitializationRequest {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Exact engine and Rust SDK target.
    pub target: TargetIdentity,
    /// Engine-selected module identity and workspace-relative root.
    pub module: ModuleOperationInput,
    /// Cargo package name used only when a new package is required.
    pub package_name: StableCoordinate,
    /// Immutable dependency emitted into the selected Cargo manifest.
    pub sdk_dependency: PublishedSdkDependency,
}

/// Complete non-secret input to standalone-client project initialization.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientInitializationRequest {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Exact engine and Rust SDK target.
    pub target: TargetIdentity,
    /// Confined workspace-relative client root selected by the engine.
    pub client_root: RelativeOperationPath,
    /// Deterministic package name used only when a new Cargo package is required.
    pub package_name: CargoPackageName,
    /// Immutable dependency emitted into the selected Cargo manifest.
    pub sdk_dependency: PublishedSdkDependency,
}

/// Closed request accepted by the private `execute` command.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "request_kind", content = "request", rename_all = "kebab-case")]
pub enum EngineExecutionRequest {
    /// Initialize or adopt one engine-selected Rust module project.
    InitializeModule(InitializationRequest),
    /// Initialize or adopt one standalone Rust client project.
    InitializeClient(ClientInitializationRequest),
    /// Execute one of the four schema-driven generation operations.
    Generate(OperationRequest),
}

/// Result class emitted by the private operation runner.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ExecutionResultKind {
    /// SDK-owned Cargo and starter-source initialization changes.
    Initialization,
    /// Standalone-client Cargo scaffold and semantic project amendments.
    ClientInitialization,
    /// One generated operation plus its durable ownership manifest.
    Generation,
}

/// Canonical data-only result consumed by the Go engine adapter.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExecutionResult {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Result class selected by the request variant.
    pub kind: ExecutionResultKind,
    /// Confined result subtree selected by the adapter.
    pub output_root: RelativeOperationPath,
    /// Durable generation manifest, absent for initialization-only changes.
    pub operation_manifest: Option<RelativeOperationPath>,
    /// Explicit generated VCS paths returned to the engine.
    pub vcs_generated: BTreeSet<RelativeOperationPath>,
    /// Explicit ignored VCS paths returned to the engine.
    pub vcs_ignored: BTreeSet<RelativeOperationPath>,
}

/// Class of one generator-owned candidate artifact.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ArtifactKind {
    /// Generated Rust source.
    RustSource,
    /// Generated Cargo manifest or fragment.
    CargoManifest,
    /// Canonical JSON control artifact.
    ControlManifest,
    /// Line-preserving VCS policy file.
    VcsPolicy,
}

/// Explicit generator ownership class; user-owned content is deliberately absent.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ArtifactOwnership {
    /// The operation manifest authorizes replacement after digest verification.
    Generator,
}

/// In-memory artifact candidate returned by the pure operation planner.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CandidateArtifact {
    /// Semantic artifact class.
    pub kind: ArtifactKind,
    /// Candidate bytes; no filesystem publication has occurred.
    pub content: Vec<u8>,
    /// Explicit ownership authority.
    pub ownership: ArtifactOwnership,
}

/// Maximum number of pure projection passes admitted by the runner.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ProjectionPassLimit {
    /// A single projection pass.
    One,
    /// One initial pass and one convergence pass.
    Two,
}

/// Closed, shell-free post-work plan.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "action", rename_all = "kebab-case", deny_unknown_fields)]
pub enum PostWorkPlan {
    /// Format exactly the generator-owned Rust source set.
    FormatRust {
        /// Exact selected toolchain.
        toolchain: ExactRustToolchain,
        /// Canonically ordered owned source paths.
        files: BTreeSet<RelativeOperationPath>,
    },
    /// Generate a lockfile for one engine-selected manifest.
    GenerateLockfile {
        /// Manifest selected by project discovery.
        manifest_path: RelativeOperationPath,
    },
    /// Verify one locked Cargo project through versioned metadata.
    VerifyLockedMetadata {
        /// Manifest selected by project discovery.
        manifest_path: RelativeOperationPath,
    },
}

/// Complete deterministic result of pure operation projection.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OperationPlan {
    /// Exact target used to construct this plan.
    pub target: TargetIdentity,
    /// Selected operation.
    pub operation: OperationKind,
    /// Digest of the canonical visible schema plan.
    pub visible_schema_digest: Sha256Digest,
    /// Candidate artifacts keyed by normalized destination.
    pub artifacts: BTreeMap<RelativeOperationPath, CandidateArtifact>,
    /// Paths requiring generated VCS attributes.
    pub vcs_generated: BTreeSet<RelativeOperationPath>,
    /// Paths requiring VCS ignore entries.
    pub vcs_ignored: BTreeSet<RelativeOperationPath>,
    /// Closed post-work actions.
    pub post_work: Vec<PostWorkPlan>,
    /// Bound preventing post-work projection loops.
    pub projection_pass_limit: ProjectionPassLimit,
}

/// Current checked generation or isolated legacy runtime generation.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum GenerationMode {
    /// Consume committed, verified generated artifacts.
    CheckedGenerated,
    /// Generate privately inside runtime construction for legacy configuration.
    LegacyRuntimeCodegen,
}

/// Durable record of one completed post-work action.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PostWorkRecord {
    /// Closed action that ran.
    pub plan: PostWorkPlan,
    /// Digest of every byte affected by the action.
    pub result_digest: Sha256Digest,
}

/// Identity of the private generator that produced an operation manifest.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratorIdentity {
    /// Exact private tool version.
    pub version: ExactVersion,
    /// Digest of the immutable engine source descriptor.
    pub engine_source_digest: Sha256Digest,
}

/// Durable record for one published generator-owned artifact.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ArtifactRecord {
    /// Semantic artifact class.
    pub kind: ArtifactKind,
    /// Digest recomputed after post-work.
    pub digest: Sha256Digest,
    /// Explicit ownership authority.
    pub ownership: ArtifactOwnership,
}

/// Semantic item class owned inside an otherwise authored file.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum AmendmentKind {
    /// One Cargo manifest key or dependency declaration.
    CargoKey,
    /// One Rust module item in the selected library root.
    RustModuleItem,
    /// One digest-marked documentation region.
    DocumentationRegion,
    /// One exact line in a line-preserving VCS policy file.
    VcsPolicyLine,
}

/// Stable map key for one semantic item inside an authored file.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct AmendmentCoordinate {
    file: RelativeOperationPath,
    semantic_key: StableCoordinate,
}

impl AmendmentCoordinate {
    /// Constructs a semantic coordinate from validated file and item identities.
    #[must_use]
    pub const fn new(file: RelativeOperationPath, semantic_key: StableCoordinate) -> Self {
        Self { file, semantic_key }
    }

    /// Borrows the authored file containing the owned semantic item.
    #[must_use]
    pub const fn file(&self) -> &RelativeOperationPath {
        &self.file
    }

    /// Borrows the stable semantic item identity.
    #[must_use]
    pub const fn semantic_key(&self) -> &StableCoordinate {
        &self.semantic_key
    }
}

impl Serialize for AmendmentCoordinate {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        // Operation-relative paths reject `:`, so the first delimiter makes this
        // spelling reversible even when a semantic key itself contains `::`.
        serializer.serialize_str(&format!("{}::{}", self.file, self.semantic_key))
    }
}

impl<'de> Deserialize<'de> for AmendmentCoordinate {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        let (file, semantic_key) = value.split_once("::").ok_or_else(|| {
            serde::de::Error::custom("amendment coordinate must contain a file and semantic key")
        })?;
        Ok(Self {
            file: RelativeOperationPath::parse(file).map_err(serde::de::Error::custom)?,
            semantic_key: semantic_key.parse().map_err(serde::de::Error::custom)?,
        })
    }
}

/// Durable authority for one semantic item inside an authored file.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AmendmentRecord {
    /// Semantic amendment class.
    pub kind: AmendmentKind,
    /// Authored file containing the item.
    pub file: RelativeOperationPath,
    /// Stable item identity interpreted by the matching semantic parser.
    pub coordinate: StableCoordinate,
    /// Digest of the canonical semantic value rather than the complete file bytes.
    pub semantic_digest: Sha256Digest,
}

/// Public namespace roles retained in a generated-client ownership manifest.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientNamespaceRecord {
    /// Exact module-root Wire_Name selected under Core `Query`.
    pub module_root_wire_name: StableCoordinate,
    /// Snake-case namespace below `dagger_client`.
    pub namespace: RustIdentifier,
    /// Public extension-trait path used to enter the module root.
    pub extension_trait_path: StableCoordinate,
}

/// Durable standalone-client identity added to a generation manifest.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientManifestRecord {
    /// Exact selected module identity, including an immutable remote pin when present.
    pub module: ClientModuleIdentity,
    /// Adopted or created Cargo/Rust project identity.
    pub package: ClientProjectIdentity,
    /// Generated namespace roles; absent for an observable Core-only surface.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub namespace: Option<ClientNamespaceRecord>,
    /// Digest of the complete semantic binding catalog.
    pub binding_catalog_digest: Sha256Digest,
    /// Number of catalog bindings covered by the digest.
    pub binding_count: u64,
}

/// Complete ownership and provenance record published last by an operation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OperationManifest {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Operation which owns these artifacts.
    pub operation: OperationKind,
    /// Committed or isolated legacy generation mode.
    pub mode: GenerationMode,
    /// Exact target identity.
    pub target: TargetIdentity,
    /// Digest of the semantic operation request.
    pub input_digest: Sha256Digest,
    /// Digest of the complete visible schema.
    pub visible_schema_digest: Sha256Digest,
    /// Digest of selected module source when applicable.
    pub module_source_digest: Option<Sha256Digest>,
    /// Immutable public SDK dependency descriptor.
    pub sdk_dependency: PublishedSdkDependency,
    /// Engine-selected output subtree.
    pub output_root: RelativeOperationPath,
    /// Generator-owned artifacts keyed by normalized path.
    pub artifacts: BTreeMap<RelativeOperationPath, ArtifactRecord>,
    /// Completed post-work records in semantic execution order.
    pub post_work: Vec<PostWorkRecord>,
    /// Private generator identity.
    pub generator: GeneratorIdentity,
    /// Semantic ownership inside authored files; omitted for legacy manifests.
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub amendments: BTreeMap<AmendmentCoordinate, AmendmentRecord>,
    /// Standalone-client identity; omitted for every non-client operation and legacy baseline.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub client: Option<ClientManifestRecord>,
}

/// Immutable engine source and packaged-SDK descriptor.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EngineSourceDescriptor {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Immutable repository origin.
    pub repository: CanonicalRepositoryUrl,
    /// Full Dagger source revision.
    pub dagger_revision: FullRevision,
    /// Exact engine version.
    pub engine_version: ExactVersion,
    /// Exact public Rust SDK version.
    pub rust_sdk_version: ExactVersion,
    /// Exact Rust compiler toolchain.
    pub rust_toolchain: ExactRustToolchain,
    /// Public SDK dependency supplied to generated projects.
    pub sdk_dependency: PublishedSdkDependency,
    /// Checked core-schema identity.
    pub core_schema_digest: Sha256Digest,
    /// Digest of the private packaged-asset manifest.
    pub packaged_asset_manifest_digest: Sha256Digest,
}

/// Cargo package selected as the unique owner of module source.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CargoPackage {
    /// Opaque Cargo metadata package identity used only for equality.
    pub package_id: StableCoordinate,
    /// Cargo package name.
    pub name: StableCoordinate,
    /// Package manifest beneath the operation root.
    pub manifest_path: RelativeOperationPath,
    /// Package root beneath the operation root.
    pub package_root: RelativeOperationPath,
}

/// Exact declared or engine-default toolchain selection.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "source", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ToolchainSelection {
    /// Compatible exact declaration owned by the caller's project hierarchy.
    Declared {
        /// Exact declared version.
        toolchain: ExactRustToolchain,
        /// Declaration selected by deterministic precedence.
        declaration_path: RelativeOperationPath,
    },
    /// Exact target default because no caller declaration exists.
    TargetDefault {
        /// Exact engine-selected default version.
        toolchain: ExactRustToolchain,
    },
}

/// Cargo project after unique package discovery but before runtime verification.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DiscoveredCargoProject {
    /// Workspace root beneath the explicit operation root.
    pub workspace_root: RelativeOperationPath,
    /// Unique package which owns engine-selected module source.
    pub target_package: CargoPackage,
    /// Existing lockfile when present before initialization.
    pub lockfile: Option<RelativeOperationPath>,
    /// Declared or target-default toolchain selection.
    pub toolchain: ToolchainSelection,
}

/// Engine-approved Cargo binary target.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CargoTarget {
    /// Exact Cargo target name.
    pub name: StableCoordinate,
    /// Target source path beneath the operation root.
    pub source_path: RelativeOperationPath,
}

/// Cargo project promoted only after every reproducibility input is verified.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeCargoProject {
    /// Discovery result retained without reinterpretation.
    pub discovered: DiscoveredCargoProject,
    /// Engine-approved binary target.
    pub target_binary: CargoTarget,
    /// Required committed lockfile.
    pub lockfile: RelativeOperationPath,
    /// Exact verified compiler toolchain.
    pub toolchain: ExactRustToolchain,
    /// Digest of the compatible operation manifest.
    pub operation_manifest_digest: Sha256Digest,
}

/// Current checked runtime or private legacy code-generation runtime.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum RuntimeCodegenMode {
    /// Build only from committed, verified generated files.
    CheckedGenerated,
    /// Regenerate privately inside runtime construction.
    LegacyRuntimeCodegen,
}

/// Complete non-secret semantic identity known before runtime compilation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeProvenanceInput {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Immutable engine source and dependency selection.
    pub engine_source: EngineSourceDescriptor,
    /// Exact compiler toolchain.
    pub toolchain: ExactRustToolchain,
    /// Digest-pinned clean runtime base.
    pub base_image_digest: Sha256Digest,
    /// Committed Cargo lockfile digest.
    pub lockfile_digest: Sha256Digest,
    /// Selected module source digest.
    pub module_source_digest: Sha256Digest,
    /// Compatible generated-operation manifest digest.
    pub operation_manifest_digest: Sha256Digest,
    /// Exact compilation target.
    pub target: RustTarget,
    /// Checked or isolated legacy runtime mode.
    pub mode: RuntimeCodegenMode,
}

/// Final runtime provenance completed only after hashing the built binary.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeProvenance {
    /// Pre-build semantic identity.
    pub input: RuntimeProvenanceInput,
    /// Digest of the final post-strip runtime executable.
    pub binary_digest: Sha256Digest,
}

/// Immutable container and path policy packaged beside the private adapter.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimePolicy {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Digest-pinned Rust build image used for operations and compilation.
    pub build_image: StableCoordinate,
    /// Digest-pinned clean runtime base image.
    pub runtime_base_image: StableCoordinate,
    /// Digest component of the clean runtime base image.
    pub runtime_base_digest: Sha256Digest,
    /// Rust target selected for Linux AMD64 engines.
    pub linux_amd64_target: RustTarget,
    /// Rust target selected for Linux ARM64 engines.
    pub linux_arm64_target: RustTarget,
    /// Fixed SDK-owned Cargo target directory inside the build container.
    pub cargo_target_dir: StableCoordinate,
    /// Fixed post-build binary path inside the build container.
    pub runtime_binary_path: StableCoordinate,
    /// Fixed executable path in the clean runtime image.
    pub runtime_install_path: StableCoordinate,
    /// Fixed provenance path in the clean runtime image.
    pub provenance_install_path: StableCoordinate,
}

/// Closed runtime verification input constructed from engine-owned identities.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeVerificationRequest {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Exact engine and Rust SDK target.
    pub target: TargetIdentity,
    /// Engine-selected module identity.
    pub module: ModuleOperationInput,
    /// Checked committed generation or private legacy generation.
    pub mode: RuntimeCodegenMode,
    /// Canonical generated ownership manifest beneath the operation root.
    pub operation_manifest: RelativeOperationPath,
    /// Exact clean runtime base digest selected from packaged policy.
    pub base_image_digest: Sha256Digest,
    /// Exact Rust compilation target selected from packaged policy.
    pub rust_target: RustTarget,
}

/// Canonical pre-build contract emitted only after runtime verification succeeds.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeBuildPlan {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Fully verified Cargo project typestate.
    pub project: RuntimeCargoProject,
    /// Checked or isolated legacy generation mode.
    pub mode: RuntimeCodegenMode,
    /// Exact generation manifest verified against project bytes.
    pub manifest: OperationManifest,
    /// Runner-authored Cargo arguments; the executable remains fixed in the adapter.
    pub cargo_args: Vec<String>,
    /// Fixed binary path relative to the SDK-owned Cargo target directory.
    pub binary_relative_path: RelativeOperationPath,
    /// Complete non-secret provenance known before compilation.
    pub provenance_input: RuntimeProvenanceInput,
}

/// One private asset packaged into built-in Rust SDK content.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PackagedAsset {
    /// Canonical content-root path.
    pub path: RelativeOperationPath,
    /// Digest of exact packaged bytes or canonical directory inventory.
    pub digest: Sha256Digest,
    /// Whether the asset must be executable in packaged content.
    pub executable: bool,
}

/// Canonical inventory of every payload asset included in Rust SDK content.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PackagedAssetManifest {
    /// Wire-format revision.
    pub format_version: FormatVersion,
    /// Assets keyed by the same path carried inside each record.
    pub assets: BTreeMap<RelativeOperationPath, PackagedAsset>,
}

/// Exact engine/runtime coordinates carried by integration evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EngineEvidenceSubject {
    /// Exact target identity.
    pub target: TargetIdentity,
    /// Immutable engine descriptor digest.
    pub engine_source_digest: Sha256Digest,
    /// Packaged private asset digest.
    pub packaged_assets_digest: Sha256Digest,
    /// Public SDK dependency observed by generated projects.
    pub sdk_dependency: PublishedSdkDependency,
    /// Exact Rust compiler toolchain.
    pub rust_toolchain: ExactRustToolchain,
    /// Exact operation request identities proved by this subject.
    pub operation_input_digests: BTreeSet<Sha256Digest>,
    /// Exact operation manifest identities proved by this subject.
    pub operation_manifest_digests: BTreeSet<Sha256Digest>,
}
