//! Pure operation inputs and deterministic renderer outputs.
//!
//! These values describe semantics only. They contain no host paths, file handles,
//! processes, engine sessions, or publication authority; the private runner translates
//! its validated wire models into this smaller compiler contract.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

use serde::{Deserialize, Serialize};

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
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
    /// Render the checked private protocol-probe entrypoint.
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

/// The one private TypeDef accepted by the bounded entrypoint renderer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EntrypointInput {
    object_name: String,
    function_name: String,
    return_scalar: String,
    result_json: String,
}

/// Canonical bytes of the sole private protocol probe accepted by the renderer.
pub const CHECKED_ENTRYPOINT_JSON: &[u8] = include_bytes!("../../assets/protocol-probe.json");

/// SHA-256 identity of [`CHECKED_ENTRYPOINT_JSON`].
pub const CHECKED_ENTRYPOINT_SHA256: &str =
    "sha256:ed6bc98ef581d820dc571a9c8dc52e1948f2f70651a7117f7ea507e705dbd374";

#[derive(Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
struct EntrypointDocument {
    format_version: u32,
    object_name: String,
    function_name: String,
    return_scalar: String,
    result_json: String,
}

impl EntrypointInput {
    /// Decodes and compares the document to the checked protocol probe.
    pub fn decode_checked(bytes: &[u8]) -> Result<Self, DiagnosticSet> {
        let document: EntrypointDocument = serde_json::from_slice(bytes).map_err(|_| {
            DiagnosticSet::one(operation_diagnostic(
                DiagnosticCode::EntrypointTypeDefInvalid,
                "operation.entrypoint",
                "entrypoint TypeDef document is not valid strict JSON",
            ))
        })?;
        if document != checked_entrypoint_document() {
            return Err(DiagnosticSet::one(operation_diagnostic(
                DiagnosticCode::EntrypointTypeDefInvalid,
                "operation.entrypoint",
                "entrypoint TypeDef does not match the checked private probe",
            )));
        }
        Ok(Self {
            object_name: document.object_name,
            function_name: document.function_name,
            return_scalar: document.return_scalar,
            result_json: document.result_json,
        })
    }

    /// Returns the canonical checked TypeDef bytes used by fixtures and packaging.
    pub fn checked_bytes() -> Result<Vec<u8>, DiagnosticSet> {
        Ok(CHECKED_ENTRYPOINT_JSON.to_vec())
    }

    #[must_use]
    pub(crate) fn object_name(&self) -> &str {
        &self.object_name
    }

    #[must_use]
    pub(crate) fn function_name(&self) -> &str {
        &self.function_name
    }

    #[must_use]
    pub(crate) fn return_scalar(&self) -> &str {
        &self.return_scalar
    }

    #[must_use]
    pub(crate) fn result_json(&self) -> &str {
        &self.result_json
    }
}

fn checked_entrypoint_document() -> EntrypointDocument {
    EntrypointDocument {
        format_version: 1,
        object_name: "RustSdkProtocolProbe".to_owned(),
        function_name: "probe".to_owned(),
        return_scalar: "String".to_owned(),
        result_json: "\"rust-sdk-protocol-ok\"".to_owned(),
    }
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
    /// Checked TypeDef input, valid only for entrypoint generation.
    pub entrypoint: Option<&'a EntrypointInput>,
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
    /// Private protocol-probe content only.
    ProtocolProbe,
}

/// One generated Cargo binary target planned without mutating a caller manifest.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CargoBinaryTarget {
    /// Stable Cargo target name used by the runtime builder.
    pub name: String,
    /// Package-relative generated source path.
    pub path: RelativeOperationPath,
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
    pub(crate) entrypoint: Option<EntrypointInput>,
    pub(crate) artifacts: BTreeMap<RelativeOperationPath, CandidateArtifact>,
    pub(crate) post_work: Vec<PostWorkPlan>,
    pub(crate) vcs_generated: BTreeSet<RelativeOperationPath>,
    pub(crate) vcs_ignored: BTreeSet<RelativeOperationPath>,
    pub(crate) projection_pass_limit: u8,
    pub(crate) content_domain: ContentDomain,
    pub(crate) client_generation: Option<ClientGenerationMetadata>,
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

    /// Returns the checked private TypeDef when entrypoint generation was selected.
    #[must_use]
    pub const fn entrypoint(&self) -> Option<&EntrypointInput> {
        self.entrypoint.as_ref()
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
