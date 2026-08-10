//! Canonical operational, project, provenance, and packaged-asset models.
//!
//! These data-only types are the private contract shared by the Rust operation tool,
//! the thin engine adapter, and completeness evidence. They contain no ambient paths,
//! credentials, command strings, process handles, or Dagger objects. BTree collections
//! make semantically unordered data deterministic before canonical encoding.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

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
    /// Private entrypoint TypeDef document, valid only for entrypoint generation.
    pub entrypoint_type_defs: Option<SchemaInput>,
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
