//! Stable, credential-safe diagnostics for private engine operations.
//!
//! The engine adapter needs machine-readable failure classes without retaining host
//! paths, command output, or caller source. Diagnostics therefore carry only a closed
//! code, a normalized operation-relative coordinate, and a bounded sanitized message.

use std::fmt;

use serde::{Deserialize, Serialize};
use thiserror::Error;

const MAX_MESSAGE_BYTES: usize = 4 * 1024;
const MAX_COORDINATE_CHARS: usize = 512;

/// Stable machine-readable failure classes emitted by the private runner.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum EngineDiagnosticCode {
    /// The packaged engine descriptor is malformed or internally inconsistent.
    SdkManifestInvalid,
    /// Packaged SDK content is incomplete, mutable, or has an invalid asset identity.
    PackagedAssetInvalid,
    /// A shipped dependency or asset is absent from the locked security audit inputs.
    SecurityAuditIncomplete,
    /// Cargo could not locate the requested manifest.
    CargoManifestMissing,
    /// Cargo or the format-preserving editor rejected a manifest.
    CargoManifestInvalid,
    /// No workspace member owns the selected module source.
    CargoPackageMissing,
    /// More than one workspace member claims the selected module source.
    CargoPackageAmbiguous,
    /// An existing SDK dependency differs from the immutable descriptor.
    SdkDependencyConflict,
    /// A dependency uses a mutable or local source.
    SdkDependencyMutable,
    /// Cargo dependency resolution or metadata execution failed.
    DependencyResolutionFailed,
    /// The selected Rust toolchain is below the SDK MSRV.
    ToolchainUnsupported,
    /// A toolchain declaration is moving, ambiguous, or otherwise unpinned.
    ToolchainNonReproducible,
    /// A lexical path escapes its explicit operation root.
    OutputPathEscape,
    /// A symlink or alias crosses the operation-root boundary.
    OutputSymlinkEscape,
    /// Existing bytes are not authorized by the prior operation manifest.
    OwnershipConflict,
    /// The prior operation manifest is stale or incompatible.
    OperationManifestStale,
    /// A process or post-work action is outside the closed allowlist.
    PostWorkRejected,
    /// A second projection pass did not converge.
    GenerationNonConvergent,
    /// Rendering or non-formatting post-work failed.
    GenerationFailed,
    /// Rust formatting failed.
    FormatFailed,
    /// Failure-atomic publication could not complete.
    PublicationFailed,
    /// Rollback could not fully restore the prior tree.
    RollbackFailed,
    /// Operation-specific input is absent, forbidden, or invalid.
    OperationInputInvalid,
    /// Standalone-client initialization input or scaffold state is invalid.
    ClientInitializationInvalid,
    /// A stored remote client pin differs from the resolved module revision.
    ClientPinMismatch,
    /// Existing Cargo or toolchain policy conflicts with standalone-client adoption.
    ClientProjectConflict,
    /// Two managed standalone-client roots overlap or alias one another.
    ClientRootOverlap,
    /// A generated standalone-client fixture failed its scoped Rust phase.
    ClientFixtureFailed,
    /// Standalone-client checkpoint evidence is missing, stale, or incomplete.
    ClientCheckpointEvidenceInvalid,
    /// A child process was cancelled and reaped.
    OperationCancelled,
    /// Checked runtime generation is missing a required committed artifact.
    GeneratedMissing,
    /// Checked runtime generation differs from its committed ownership manifest.
    GeneratedStale,
    /// Checked runtime construction has no committed Cargo lockfile.
    LockfileMissing,
    /// The committed Cargo lockfile does not resolve the selected manifest.
    LockfileStale,
    /// The generated `dagger-module` target is absent or caller-controlled.
    RuntimeTargetInvalid,
    /// Cargo compilation or post-build processing failed.
    RuntimeBuildFailed,
    /// Nested session metadata is absent or malformed.
    RuntimeSessionInvalid,
    /// The current engine call context is malformed or unsupported.
    RuntimeProtocolInvalid,
    /// The engine rejected fixed module registration.
    RuntimeProtocolFailed,
    /// The engine rejected the fixed function result.
    ResultReportFailed,
    /// A runtime diagnostic could not be proven free of credential material.
    DiagnosticRedactionFailed,
}

impl EngineDiagnosticCode {
    /// Complete private diagnostic taxonomy in declaration order.
    pub const ALL: &'static [Self] = &[
        Self::SdkManifestInvalid,
        Self::PackagedAssetInvalid,
        Self::SecurityAuditIncomplete,
        Self::CargoManifestMissing,
        Self::CargoManifestInvalid,
        Self::CargoPackageMissing,
        Self::CargoPackageAmbiguous,
        Self::SdkDependencyConflict,
        Self::SdkDependencyMutable,
        Self::DependencyResolutionFailed,
        Self::ToolchainUnsupported,
        Self::ToolchainNonReproducible,
        Self::OutputPathEscape,
        Self::OutputSymlinkEscape,
        Self::OwnershipConflict,
        Self::OperationManifestStale,
        Self::PostWorkRejected,
        Self::GenerationNonConvergent,
        Self::GenerationFailed,
        Self::FormatFailed,
        Self::PublicationFailed,
        Self::RollbackFailed,
        Self::OperationInputInvalid,
        Self::ClientInitializationInvalid,
        Self::ClientPinMismatch,
        Self::ClientProjectConflict,
        Self::ClientRootOverlap,
        Self::ClientFixtureFailed,
        Self::ClientCheckpointEvidenceInvalid,
        Self::OperationCancelled,
        Self::GeneratedMissing,
        Self::GeneratedStale,
        Self::LockfileMissing,
        Self::LockfileStale,
        Self::RuntimeTargetInvalid,
        Self::RuntimeBuildFailed,
        Self::RuntimeSessionInvalid,
        Self::RuntimeProtocolInvalid,
        Self::RuntimeProtocolFailed,
        Self::ResultReportFailed,
        Self::DiagnosticRedactionFailed,
    ];
}

impl fmt::Display for EngineDiagnosticCode {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        let encoded = serde_json::to_string(self).map_err(|_| fmt::Error)?;
        formatter.write_str(encoded.trim_matches('"'))
    }
}

/// One deterministic private diagnostic safe to forward through the engine adapter.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize, Error)]
#[error("{code}{coordinate_text}: {message}")]
pub struct EngineDiagnostic {
    /// Stable error class.
    pub code: EngineDiagnosticCode,
    /// Normalized non-secret operation coordinate.
    pub coordinate: Option<String>,
    /// Bounded caller-actionable explanation.
    pub message: String,
    /// Stable underlying failure classes retained without implementation detail.
    pub causes: Vec<EngineDiagnosticCode>,
    #[serde(skip)]
    coordinate_text: String,
}

impl EngineDiagnostic {
    /// Constructs a diagnostic after sanitizing and bounding caller-controlled text.
    #[must_use]
    pub fn new(
        code: EngineDiagnosticCode,
        coordinate: Option<impl AsRef<str>>,
        message: impl AsRef<str>,
    ) -> Self {
        let coordinate = coordinate.map(|value| sanitize_coordinate(value.as_ref()));
        let coordinate_text = coordinate
            .as_ref()
            .map(|value| format!(" [{value}]"))
            .unwrap_or_default();
        Self {
            code,
            coordinate,
            message: sanitize_message(message.as_ref()),
            causes: Vec::new(),
            coordinate_text,
        }
    }

    /// Retains one underlying stable failure class in deterministic order.
    #[must_use]
    pub fn with_cause(mut self, code: EngineDiagnosticCode) -> Self {
        self.causes.push(code);
        self.causes.sort();
        self.causes.dedup();
        self
    }

    /// Renders one canonical line for bounded stderr output.
    #[must_use]
    pub fn render(&self) -> String {
        self.to_string()
    }
}

fn sanitize_coordinate(value: &str) -> String {
    let sanitized = value
        .chars()
        .filter(|character| !character.is_control())
        .take(MAX_COORDINATE_CHARS)
        .collect::<String>();
    if coordinate_is_private(&sanitized) {
        "[REDACTED]".to_owned()
    } else {
        sanitized
    }
}

fn coordinate_is_private(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    value.starts_with('/')
        || value.starts_with("~/")
        || value.as_bytes().get(1).is_some_and(|byte| *byte == b':')
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
        .any(|marker| lower.contains(marker))
}

fn sanitize_message(value: &str) -> String {
    let mut sanitized = value
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
