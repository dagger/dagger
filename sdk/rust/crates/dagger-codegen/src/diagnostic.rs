//! Stable, caller-safe diagnostics produced by code generation.
//!
//! Diagnostics are data rather than formatted implementation errors. This keeps bad
//! schema input from exposing host paths and gives automation a stable code and
//! coordinate to act on.

use std::fmt;

use serde::{Deserialize, Serialize};
use thiserror::Error;

const MAX_COORDINATE_CHARS: usize = 512;
const MAX_MESSAGE_BYTES: usize = 4 * 1024;

/// A stable machine-readable generator diagnostic code.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum DiagnosticCode {
    /// The exact target descriptor is malformed or differs from the approved target.
    TargetIdentityInvalid,
    /// The schema bytes do not match the digest in the target descriptor.
    SchemaDigestMismatch,
    /// The schema envelope or operation roots are invalid.
    SchemaRootInvalid,
    /// A public schema kind is not supported by the core client.
    SchemaTypeUnsupported,
    /// A name, reference, or required schema member is invalid.
    SchemaReferenceInvalid,
    /// A reviewed core coordinate is absent from an engine-visible schema.
    SchemaCoreCoordinateMissing,
    /// A reviewed core coordinate changed semantic shape in an engine-visible schema.
    SchemaCoreCoordinateIncompatible,
    /// A recursive type wrapper is malformed or exceeds its bound.
    SchemaWrapperInvalid,
    /// A GraphQL default literal is malformed or has the wrong type.
    SchemaDefaultInvalid,
    /// A directive definition or application is invalid.
    SchemaDirectiveArgumentInvalid,
    /// A schema field was not assigned a projection.
    SchemaFieldUnmapped,
    /// A schema argument was not assigned a projection.
    SchemaArgumentUnmapped,
    /// An input-object field was not assigned a projection.
    SchemaInputFieldUnmapped,
    /// An enum value was not assigned a projection.
    SchemaEnumValueUnmapped,
    /// A directive was not assigned an explicit policy.
    SchemaDirectiveUnmapped,
    /// An object-handle mapping is invalid.
    ObjectHandleMappingInvalid,
    /// A list re-entry type is invalid.
    ListReentryTypeInvalid,
    /// An expected-type mapping is invalid.
    ExpectedTypeInvalid,
    /// An optional-argument mapping is invalid.
    OptionArgumentMappingInvalid,
    /// A projected name no longer matches its wire name.
    WireNameMismatch,
    /// Legacy and directive deprecation metadata disagree.
    DeprecationDirectiveInvalid,
    /// Experimental directive metadata is invalid.
    ExperimentalDirectiveInvalid,
    /// A target-inactive directive changed or became active.
    TargetInactiveDirectiveChanged,
    /// A Rust identifier cannot be represented safely.
    RustNameInvalid,
    /// Two schema coordinates project to the same Rust identifier.
    RustNameCollision,
    /// Generated documentation is invalid.
    GeneratedDocumentationInvalid,
    /// Generated provenance is invalid.
    GeneratedProvenanceInvalid,
    /// A capability has no generated binding.
    CapabilityBindingMissing,
    /// A capability has more than one generated binding.
    CapabilityBindingDuplicate,
    /// A capability implementation fingerprint changed unexpectedly.
    CapabilityFingerprintMismatch,
    /// The pinned formatter rejected generated source.
    GeneratedFormatFailed,
    /// Checked generated output differs from the candidate.
    GeneratedOutputDrift,
    /// Atomic generated-output publication failed.
    GeneratedPublicationFailed,
    /// A serialized engine operation selector is not part of the closed set.
    OperationUnknown,
    /// A selected operation is missing one of its required semantic inputs.
    OperationInputMissing,
    /// A selected operation received an input owned by a different operation.
    OperationInputForbidden,
    /// Two renderer outputs attempted to own the same normalized artifact path.
    OperationArtifactCollision,
    /// Client-generation metadata contains an invalid or duplicate relative path.
    RequiredHostFileInvalid,
    /// The selected module root is missing, promoted, duplicated, or malformed.
    ClientModuleRootInvalid,
    /// A non-Core coordinate lies outside the selected module's reachable closure.
    ClientSchemaScopeInvalid,
}

impl DiagnosticCode {
    /// Complete generator diagnostic taxonomy in declaration order.
    pub const ALL: &'static [Self] = &[
        Self::TargetIdentityInvalid,
        Self::SchemaDigestMismatch,
        Self::SchemaRootInvalid,
        Self::SchemaTypeUnsupported,
        Self::SchemaReferenceInvalid,
        Self::SchemaCoreCoordinateMissing,
        Self::SchemaCoreCoordinateIncompatible,
        Self::SchemaWrapperInvalid,
        Self::SchemaDefaultInvalid,
        Self::SchemaDirectiveArgumentInvalid,
        Self::SchemaFieldUnmapped,
        Self::SchemaArgumentUnmapped,
        Self::SchemaInputFieldUnmapped,
        Self::SchemaEnumValueUnmapped,
        Self::SchemaDirectiveUnmapped,
        Self::ObjectHandleMappingInvalid,
        Self::ListReentryTypeInvalid,
        Self::ExpectedTypeInvalid,
        Self::OptionArgumentMappingInvalid,
        Self::WireNameMismatch,
        Self::DeprecationDirectiveInvalid,
        Self::ExperimentalDirectiveInvalid,
        Self::TargetInactiveDirectiveChanged,
        Self::RustNameInvalid,
        Self::RustNameCollision,
        Self::GeneratedDocumentationInvalid,
        Self::GeneratedProvenanceInvalid,
        Self::CapabilityBindingMissing,
        Self::CapabilityBindingDuplicate,
        Self::CapabilityFingerprintMismatch,
        Self::GeneratedFormatFailed,
        Self::GeneratedOutputDrift,
        Self::GeneratedPublicationFailed,
        Self::OperationUnknown,
        Self::OperationInputMissing,
        Self::OperationInputForbidden,
        Self::OperationArtifactCollision,
        Self::RequiredHostFileInvalid,
        Self::ClientModuleRootInvalid,
        Self::ClientSchemaScopeInvalid,
    ];
}

impl fmt::Display for DiagnosticCode {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        let serialized = serde_json::to_string(self).map_err(|_| fmt::Error)?;
        formatter.write_str(serialized.trim_matches('"'))
    }
}

/// A normalized, host-independent location within generator input or output.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct DiagnosticCoordinate(String);

impl DiagnosticCoordinate {
    /// Creates a coordinate after removing control characters from caller input.
    #[must_use]
    pub fn new(value: impl AsRef<str>) -> Self {
        let normalized = value
            .as_ref()
            .chars()
            .filter(|character| !character.is_control())
            .take(MAX_COORDINATE_CHARS)
            .collect::<String>();
        let lower = normalized.to_ascii_lowercase();
        let private = normalized.starts_with('/')
            || normalized.starts_with("~/")
            || normalized
                .as_bytes()
                .get(1)
                .is_some_and(|byte| *byte == b':')
            || [
                "://",
                "git@",
                "authorization",
                "bearer ",
                "token=",
                "password=",
                "session_token",
            ]
            .iter()
            .any(|marker| lower.contains(marker));
        let normalized = if private {
            "[REDACTED]".to_owned()
        } else {
            normalized
        };
        Self(normalized)
    }

    /// Borrows the normalized coordinate.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for DiagnosticCoordinate {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

/// Additional coordinate context associated with a diagnostic.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct RelatedCoordinate {
    /// The related normalized coordinate.
    pub coordinate: DiagnosticCoordinate,
    /// Why the coordinate is relevant.
    pub relationship: String,
}

/// One structured generator diagnostic.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct Diagnostic {
    /// Stable machine-readable failure code.
    pub code: DiagnosticCode,
    /// Primary schema, target, artifact, or capability coordinate.
    pub coordinate: Option<DiagnosticCoordinate>,
    /// Caller-actionable explanation without host implementation details.
    pub message: String,
    /// Other coordinates needed to understand a conflict.
    pub related: Vec<RelatedCoordinate>,
}

impl Diagnostic {
    /// Creates a diagnostic with no related-coordinate context.
    #[must_use]
    pub fn new(
        code: DiagnosticCode,
        coordinate: Option<DiagnosticCoordinate>,
        message: impl Into<String>,
    ) -> Self {
        Self {
            code,
            coordinate,
            message: sanitize_message(message.into()),
            related: Vec::new(),
        }
    }

    /// Adds related-coordinate context.
    #[must_use]
    pub fn with_related(mut self, related: RelatedCoordinate) -> Self {
        self.related.push(related);
        self.related.sort();
        self.related.dedup();
        self
    }
}

impl fmt::Display for Diagnostic {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{}", self.code)?;
        if let Some(coordinate) = &self.coordinate {
            write!(formatter, " [{coordinate}]")?;
        }
        write!(formatter, ": {}", self.message)
    }
}

/// A non-empty, deterministically ordered collection of diagnostics.
#[derive(Clone, Debug, Eq, PartialEq, Error)]
#[error("{rendered}")]
pub struct DiagnosticSet {
    diagnostics: Vec<Diagnostic>,
    rendered: String,
}

impl DiagnosticSet {
    /// Sorts and de-duplicates a non-empty diagnostic collection.
    pub fn new(mut diagnostics: Vec<Diagnostic>) -> Option<Self> {
        diagnostics.sort();
        diagnostics.dedup();
        if diagnostics.is_empty() {
            return None;
        }
        let rendered = diagnostics
            .iter()
            .map(ToString::to_string)
            .collect::<Vec<_>>()
            .join("\n");
        Some(Self {
            diagnostics,
            rendered,
        })
    }

    /// Creates a diagnostic set containing exactly one diagnostic.
    #[must_use]
    pub fn one(diagnostic: Diagnostic) -> Self {
        let rendered = diagnostic.to_string();
        Self {
            diagnostics: vec![diagnostic],
            rendered,
        }
    }

    /// Borrows diagnostics in stable code/coordinate/message order.
    #[must_use]
    pub fn diagnostics(&self) -> &[Diagnostic] {
        &self.diagnostics
    }

    /// Returns whether this set contains a particular stable code.
    #[must_use]
    pub fn contains(&self, code: DiagnosticCode) -> bool {
        self.diagnostics
            .iter()
            .any(|diagnostic| diagnostic.code == code)
    }
}

/// A broad compatibility category for the transitional single-file renderer.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DiagnosticKind {
    /// Raw introspection input could not be decoded.
    Decode,
    /// The target contract is invalid or unsupported.
    Target,
    /// The schema violates a canonicalization invariant.
    Schema,
    /// Rust syntax could not be constructed or validated.
    Render,
}

/// A compatibility error used only by the transitional renderer.
#[derive(Debug, Error)]
#[error("{kind:?}: {message}")]
pub struct CodegenError {
    kind: DiagnosticKind,
    message: String,
}

impl CodegenError {
    /// Creates a compatibility diagnostic with a stable category.
    #[must_use]
    pub fn new(kind: DiagnosticKind, message: impl Into<String>) -> Self {
        Self {
            kind,
            message: sanitize_message(message.into()),
        }
    }

    /// Returns the broad compatibility category.
    #[must_use]
    pub const fn kind(&self) -> DiagnosticKind {
        self.kind
    }
}

fn sanitize_message(message: String) -> String {
    let mut sanitized = message
        .chars()
        .filter(|character| !character.is_control() || *character == '\n')
        .collect::<String>();
    for marker in [
        "https://",
        "http://",
        "ssh://",
        "git@",
        "Authorization:",
        "authorization:",
        "Bearer ",
        "bearer ",
        "token=",
        "password=",
    ] {
        while let Some(start) = sanitized.find(marker) {
            let search_from = start + marker.len();
            let end = sanitized[search_from..]
                .find(char::is_whitespace)
                .map_or(sanitized.len(), |offset| search_from + offset);
            sanitized.replace_range(start..end, "[REDACTED]");
        }
    }
    if sanitized.len() > MAX_MESSAGE_BYTES {
        let mut boundary = MAX_MESSAGE_BYTES;
        while !sanitized.is_char_boundary(boundary) {
            boundary -= 1;
        }
        sanitized.truncate(boundary);
        sanitized.push_str("...[truncated]");
    }
    sanitized
}
