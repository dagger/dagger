//! Pure operation inputs and deterministic renderer outputs.
//!
//! These values describe semantics only. They contain no host paths, file handles,
//! processes, engine sessions, or publication authority; the private runner translates
//! its validated wire models into this smaller compiler contract.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

use serde::{Deserialize, Serialize};

use crate::client::{ClientNamespaceRecord, ClientProjectIdentity};
use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::module::{ModuleSourceSnapshot, Sha256Digest as ModuleSha256Digest};
use crate::target::CodegenTarget;

use super::metadata::ClientGenerationMetadata;
use super::visible::VisibleSchemaPlan;

/// Closed set of pure code-generation operations.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum OperationKind {
    /// Render reusable visible-schema library bindings.
    GenerateLibrary,
    /// Render module-owned bindings and the private runtime entrypoint.
    GenerateModule,
    /// Render the bounded standalone client baseline.
    GenerateClient,
    /// Render the generic descriptor-bound module entrypoint.
    GenerateEntrypoint,
}

impl OperationKind {
    /// Decodes the stable selector spelling without permitting a fallback variant.
    pub fn decode(value: &str) -> Result<Self, DiagnosticSet> {
        match value {
            "generate-library" => Ok(Self::GenerateLibrary),
            "generate-module" => Ok(Self::GenerateModule),
            "generate-client" => Ok(Self::GenerateClient),
            "generate-entrypoint" => Ok(Self::GenerateEntrypoint),
            _ => Err(DiagnosticSet::one(operation_diagnostic(
                DiagnosticCode::OperationUnknown,
                "operation.selector",
                "operation selector is not supported",
            ))),
        }
    }

    /// Returns the stable selector spelling used by diagnostics and manifests.
    #[must_use]
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::GenerateLibrary => "generate-library",
            Self::GenerateModule => "generate-module",
            Self::GenerateClient => "generate-client",
            Self::GenerateEntrypoint => "generate-entrypoint",
        }
    }
}

/// A canonical lexical path beneath an operation-selected root.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(transparent)]
pub struct RelativeOperationPath(String);

impl RelativeOperationPath {
    /// Constructs a path from a compile-time reviewed generator constant.
    pub(super) fn from_reviewed_static(value: &'static str) -> Self {
        Self(value.to_owned())
    }

    /// Parses a non-empty, slash-normalized relative path.
    pub fn parse(value: &str) -> Result<Self, DiagnosticSet> {
        let invalid = value.is_empty()
            || value.starts_with('/')
            || value.starts_with('\\')
            || value.contains('\\')
            || value.chars().any(char::is_control)
            || value.split('/').any(|component| {
                component.is_empty()
                    || component == "."
                    || component == ".."
                    || component.ends_with(':')
            });
        if invalid {
            return Err(DiagnosticSet::one(operation_diagnostic(
                DiagnosticCode::RequiredHostFileInvalid,
                value,
                "path must be a normalized non-empty relative path",
            )));
        }
        Ok(Self(value.to_owned()))
    }

    /// Appends a validated relative suffix without consulting a filesystem.
    pub fn join(&self, suffix: &str) -> Result<Self, DiagnosticSet> {
        let suffix = Self::parse(suffix)?;
        Self::parse(&format!("{}/{}", self.0, suffix.0))
    }

    /// Borrows the canonical slash-separated spelling.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for RelativeOperationPath {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for RelativeOperationPath {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let value = String::deserialize(deserializer)?;
        Self::parse(&value).map_err(serde::de::Error::custom)
    }
}

/// Exact module identity forwarded by the engine adapter.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleProjectionInput {
    /// Engine-normalized module name.
    pub name: String,
    /// Caller-visible module name retained for generated diagnostics.
    pub original_name: String,
    /// Scoped source subtree selected by the engine.
    pub source_subpath: RelativeOperationPath,
    /// Digest of the complete selected source.
    pub source_digest: String,
}

/// Immutable public SDK dependency rendered into a baseline Cargo project.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum PublishedSdkDependency {
    /// Exact registry release.
    Registry {
        /// Registry name; `crates-io` uses Cargo's default registry.
        registry: String,
        /// Exact version without a moving range.
        exact_version: String,
    },
    /// Credential-free repository fixed to one full revision.
    Git {
        /// Canonical HTTPS repository URL.
        url: String,
        /// Full immutable Git revision.
        revision: String,
    },
}

/// Closed Rust-owned inputs to descriptor and generated-asset compilation.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleAuthoringInput {
    /// Confined immutable source selected by the engine-side package adapter.
    pub source: ModuleSourceSnapshot,
    /// Immutable generator identity included in descriptor provenance.
    pub generator_digest: ModuleSha256Digest,
    /// Exact Cargo alias through which generated support reaches `dagger-sdk`.
    pub sdk_dependency_alias: String,
}

/// Borrowed values needed to validate and render one operation.
pub struct OperationProjectionRequest<'a> {
    /// Exact checked target.
    pub target: &'a CodegenTarget,
    /// Closed operation selector.
    pub operation: OperationKind,
    /// Complete engine-visible introspection document.
    pub visible_schema_json: &'a [u8],
    /// Exact module identity when the selector requires one.
    pub module: Option<&'a ModuleProjectionInput>,
    /// Normalized operation-owned output subtree.
    pub output: &'a RelativeOperationPath,
    /// Immutable public SDK dependency.
    pub sdk_dependency: &'a PublishedSdkDependency,
    /// Rust-owned authoring input for module and entrypoint generation.
    pub authoring: Option<&'a ModuleAuthoringInput>,
}

/// Class of one pure candidate artifact.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub enum CandidateArtifactKind {
    /// Generated Rust source.
    RustSource,
    /// Generated Cargo manifest.
    CargoManifest,
    /// Canonical control or semantic catalog data.
    ControlManifest,
}

/// One in-memory artifact; no filesystem publication has occurred.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CandidateArtifact {
    /// Artifact class used by publication and VCS policy.
    pub kind: CandidateArtifactKind,
    /// Exact candidate bytes.
    pub content: Vec<u8>,
}

/// Closed post-render work requested by a pure renderer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum PostWorkPlan {
    /// Format exactly the listed generated Rust files.
    FormatRust {
        /// Canonically ordered generator-owned source paths.
        files: BTreeSet<RelativeOperationPath>,
    },
}

/// Evidence domain attached to bounded renderer output.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ContentDomain {
    /// Reusable visible-schema binding output.
    VisibleSchemaBindings,
    /// Module code-generation hook output, including its bounded private entrypoint.
    ModuleOperation,
    /// Hook-valid baseline that does not claim sibling client-content completeness.
    EngineHookBaseline,
    /// Complete standalone-client generated subtree and semantic catalog.
    StandaloneClient,
    /// Generic descriptor-bound module entrypoint content only.
    ModuleEntrypoint,
}

/// One generated Cargo binary target planned without mutating a caller manifest.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CargoBinaryTarget {
    /// Stable Cargo target name used by the runtime builder.
    pub name: String,
    /// Package-relative generated source path.
    pub path: RelativeOperationPath,
}

/// Semantic identity emitted only by the complete standalone-client renderer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientRenderIdentity {
    /// Cargo and Rust crate names selected from the caller's bounded project snapshot.
    pub project: ClientProjectIdentity,
    /// Generated namespace roles, absent for a Core-only client.
    pub namespace: Option<ClientNamespaceRecord>,
    /// Exact module-root wire name, absent for a Core-only client.
    pub module_root_wire_name: Option<String>,
    /// Domain-separated digest of the exhaustive Core-plus-module binding catalog.
    pub binding_catalog_digest: String,
    /// Number of semantic bindings covered by the catalog digest.
    pub binding_count: u64,
}

/// Complete immutable result of pure operation projection.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OperationPlan {
    pub(crate) target: CodegenTarget,
    pub(crate) operation: OperationKind,
    pub(crate) schema: VisibleSchemaPlan,
    pub(crate) module: Option<ModuleProjectionInput>,
    pub(crate) output: RelativeOperationPath,
    pub(crate) sdk_dependency: PublishedSdkDependency,
    pub(crate) authoring: Option<ModuleAuthoringInput>,
    pub(crate) artifacts: BTreeMap<RelativeOperationPath, CandidateArtifact>,
    pub(crate) post_work: Vec<PostWorkPlan>,
    pub(crate) vcs_generated: BTreeSet<RelativeOperationPath>,
    pub(crate) vcs_ignored: BTreeSet<RelativeOperationPath>,
    pub(crate) projection_pass_limit: u8,
    pub(crate) content_domain: ContentDomain,
    pub(crate) client_generation: Option<ClientGenerationMetadata>,
    pub(crate) client_render: Option<ClientRenderIdentity>,
    pub(crate) cargo_binary: Option<CargoBinaryTarget>,
}

impl OperationPlan {
    /// Returns the exact checked target retained by the plan.
    #[must_use]
    pub const fn target(&self) -> &CodegenTarget {
        &self.target
    }

    /// Returns the closed selector which produced the plan.
    #[must_use]
    pub const fn operation(&self) -> OperationKind {
        self.operation
    }

    /// Returns the shared visible-schema plan used by the renderer.
    #[must_use]
    pub const fn schema(&self) -> &VisibleSchemaPlan {
        &self.schema
    }

    /// Returns the exact scoped module identity when the operation has one.
    #[must_use]
    pub const fn module(&self) -> Option<&ModuleProjectionInput> {
        self.module.as_ref()
    }

    /// Returns the normalized engine-selected output identity.
    #[must_use]
    pub const fn output(&self) -> &RelativeOperationPath {
        &self.output
    }

    /// Returns the immutable public SDK dependency forwarded to the renderer.
    #[must_use]
    pub const fn sdk_dependency(&self) -> &PublishedSdkDependency {
        &self.sdk_dependency
    }

    /// Returns the closed Rust authoring input when module compilation was selected.
    #[must_use]
    pub const fn authoring(&self) -> Option<&ModuleAuthoringInput> {
        self.authoring.as_ref()
    }

    /// Returns candidate artifacts in normalized path order.
    #[must_use]
    pub const fn artifacts(&self) -> &BTreeMap<RelativeOperationPath, CandidateArtifact> {
        &self.artifacts
    }

    /// Returns closed post-render actions in execution order.
    #[must_use]
    pub fn post_work(&self) -> &[PostWorkPlan] {
        &self.post_work
    }

    /// Returns paths requiring generated VCS treatment.
    #[must_use]
    pub const fn vcs_generated(&self) -> &BTreeSet<RelativeOperationPath> {
        &self.vcs_generated
    }

    /// Returns paths requiring ignore treatment.
    #[must_use]
    pub const fn vcs_ignored(&self) -> &BTreeSet<RelativeOperationPath> {
        &self.vcs_ignored
    }

    /// Returns the maximum number of pure projection passes.
    #[must_use]
    pub const fn projection_pass_limit(&self) -> u8 {
        self.projection_pass_limit
    }

    /// Returns the bounded evidence domain represented by the artifacts.
    #[must_use]
    pub const fn content_domain(&self) -> ContentDomain {
        self.content_domain
    }

    /// Returns Rust-owned required-host-file metadata for client generation.
    #[must_use]
    pub const fn client_generation(&self) -> Option<&ClientGenerationMetadata> {
        self.client_generation.as_ref()
    }

    /// Returns the complete standalone-client project and catalog identity.
    #[must_use]
    pub const fn client_render(&self) -> Option<&ClientRenderIdentity> {
        self.client_render.as_ref()
    }

    /// Returns the exact Cargo binary amendment required by this operation.
    #[must_use]
    pub const fn cargo_binary(&self) -> Option<&CargoBinaryTarget> {
        self.cargo_binary.as_ref()
    }
}

pub(crate) fn operation_diagnostic(
    code: DiagnosticCode,
    coordinate: &str,
    message: &str,
) -> Diagnostic {
    Diagnostic::new(code, Some(DiagnosticCoordinate::new(coordinate)), message)
}
