//! Locked Rust dependency, external provenance, vulnerability, and retained-evidence policy.
//!
//! Live canary and host-identity bytes remain in non-serializable values. Durable artifacts carry
//! only immutable identities, complete finding records, safe coordinates, and bounded outcomes.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::io::{Read, Seek, SeekFrom};
use std::path::{Component, Path};

use flate2::read::GzDecoder;
use rand::{RngCore as _, rngs::OsRng};
use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use sha2::{Digest as _, Sha256};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    CanonicalSet, CommitSha, Digest, NonEmptyText, RepositoryId, RepositoryRelativePath,
    SemverVersion,
};

use super::{
    AdmittedArtifact, ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, FindingId, NonZeroMillis,
    ProvenanceId,
};

const MAX_INSPECTED_BYTES: u64 = 16 * 1024 * 1024;
const MEBIBYTE: u64 = 1024 * 1024;
const PACKAGED_ARTIFACT_FILE_LIMIT: u64 = 256 * MEBIBYTE;
const PACKAGED_ARTIFACT_COMPRESSED_LIMIT: u64 = 512 * MEBIBYTE;
const PACKAGED_ARTIFACT_EXPANDED_LIMIT: u64 = 2 * 1024 * MEBIBYTE;
const PACKAGED_ARTIFACT_EXPANDED_FILE_LIMIT: u64 = 256 * MEBIBYTE;
const PACKAGED_ARTIFACT_ENTRY_LIMIT: u64 = 200_000;
const PACKAGED_ARTIFACT_PATH_LIMIT: usize = 4096;
const PACKAGED_ARTIFACT_DEPTH_LIMIT: usize = 64;
const PACKAGED_ARTIFACT_METADATA_LIMIT: u64 = MEBIBYTE;
const PACKAGED_ARTIFACT_SCANNER_ID: &[u8] = b"dagger-rust-sdk-packaged-artifact-scanner-v1";
const TRIVY_DATABASE_REVIEW_EVIDENCE: &[u8] =
    include_bytes!("../../../../completeness/evidence/trivy-db-review.json");

/// One independently resolved Cargo root and its committed lockfile.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SupportedCargoRoot {
    /// Root package or workspace manifest.
    pub manifest: RepositoryRelativePath,
    /// Lockfile resolved by `--locked` for this root.
    pub lockfile: RepositoryRelativePath,
}

/// Cargo Deny class required by the ordinary Rust security gate.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CargoDenyClass {
    /// RustSec advisory evaluation.
    Advisories,
    /// Approved-license evaluation.
    Licenses,
    /// Dependency-ban and wildcard evaluation.
    Bans,
    /// Registry and Git source evaluation.
    Sources,
}

/// GitHub workflow permission level admitted by least-privilege policy.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum WorkflowPermissionLevel {
    /// No token access.
    None,
    /// Read-only token access.
    Read,
    /// Write access, representable so admission can reject it.
    Write,
}

/// One narrow reviewed exception to workspace-wide unsafe denial.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct UnsafeExceptionObservation {
    /// Safe source coordinate.
    pub source: RepositoryRelativePath,
    /// Digest of the documented safety invariant.
    pub invariant_digest: Digest,
    /// Digest of tests exercising the invariant.
    pub test_digest: Digest,
    /// True only for a source-local allow rather than a crate-wide relaxation.
    pub narrow_allow: bool,
}

/// Complete engine-free observation of ordinary Rust dependency and automation policy.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RustDependencySecurityObservation {
    /// Durable observation format.
    pub format_version: ConformanceFormatVersion,
    /// Authored roots retained as a vector so duplicates remain visible.
    pub cargo_roots: Vec<SupportedCargoRoot>,
    /// Every committed Rust lockfile, including auxiliary checked lockfiles.
    pub committed_lockfiles: CanonicalSet<RepositoryRelativePath>,
    /// Root manifests actually resolved with `--locked`.
    pub locked_roots: CanonicalSet<RepositoryRelativePath>,
    /// Cargo Deny classes executed against the current graph.
    pub cargo_deny_classes: CanonicalSet<CargoDenyClass>,
    /// Reachable active advisories.
    pub reachable_advisories: CanonicalSet<FindingId>,
    /// Unapproved license expressions.
    pub unapproved_licenses: CanonicalSet<NonEmptyText>,
    /// Non-local wildcard dependency coordinates.
    pub unapproved_wildcards: CanonicalSet<RepositoryRelativePath>,
    /// Unknown registry or Git sources.
    pub unknown_sources: CanonicalSet<NonEmptyText>,
    /// Whether production compilation retains `unsafe_code = "deny"`.
    pub workspace_unsafe_denied: bool,
    /// Narrow explicitly proved unsafe exceptions, normally empty.
    pub unsafe_exceptions: Vec<UnsafeExceptionObservation>,
    /// Cargo roots covered by dependency automation.
    pub automated_cargo_roots: CanonicalSet<RepositoryRelativePath>,
    /// Inapplicable automation entries pretending to cover Rust.
    pub inapplicable_automation: CanonicalSet<RepositoryRelativePath>,
    /// Whether the packaged self-consumer uses immutable non-path dependencies.
    pub packaged_dependencies_immutable: bool,
    /// Explicit workflow token permissions.
    pub workflow_permissions: BTreeMap<NonEmptyText, WorkflowPermissionLevel>,
}

/// Admitted ordinary Rust security closure consumed without replay by sign-off.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RustDependencySecurityReport {
    /// Durable report format.
    pub format_version: ConformanceFormatVersion,
    /// Exact supported Cargo roots.
    pub cargo_roots: CanonicalSet<SupportedCargoRoot>,
    /// Exact committed lockfile set.
    pub committed_lockfiles: CanonicalSet<RepositoryRelativePath>,
    /// Domain-separated identity of every admitted observation.
    pub security_digest: Digest,
}

/// External input role with no generic catch-all variant.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ExternalInputRole {
    /// Rust SDK compilation image.
    ArtifactBuilderImage,
    /// Focused engine base image.
    EngineBaseImage,
    /// Exact Rust toolchain source.
    RustToolchain,
    /// Go helper toolchain image.
    GoToolchain,
    /// Provider-neutral preflight binary.
    PreflightCli,
    /// Pinned preflight smoke engine.
    PreflightEngine,
    /// Official Dagger CLI release archive.
    CliArchive,
    /// Vulnerability scanner image.
    ScannerImage,
    /// Reviewed vulnerability-database source.
    VulnerabilityDatabaseSource,
}

/// Reviewed immutable external-input provenance.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProvenanceRecord {
    /// Stable record identity.
    pub id: ProvenanceId,
    /// Role for which the input was reviewed.
    pub role: ExternalInputRole,
    /// Canonical non-personal publisher identity.
    pub publisher: NonEmptyText,
    /// Authoritative source repository.
    pub repository: RepositoryId,
    /// Immutable image digest, archive checksum, commit-content digest, or binary digest.
    pub immutable_digest: Digest,
    /// Digest of independent review evidence.
    pub review_evidence_digest: Digest,
}

/// Authored provenance list retained before duplicate and role validation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExternalProvenanceRegistryInput {
    /// Durable registry format.
    pub format_version: ConformanceFormatVersion,
    /// One record for every required role.
    pub records: Vec<ProvenanceRecord>,
}

/// Checked immutable external provenance indexed by role.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExternalProvenanceRegistry {
    /// Durable registry format.
    pub format_version: ConformanceFormatVersion,
    /// Exact one-per-role records.
    pub records: BTreeMap<ExternalInputRole, ProvenanceRecord>,
    /// Domain-separated registry identity.
    pub registry_digest: Digest,
}

/// Canonical vulnerability severity vocabulary.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum VulnerabilitySeverity {
    /// Informational or unknown-risk record retained for audit.
    Unknown,
    /// Low-severity finding.
    Low,
    /// Medium-severity finding.
    Medium,
    /// High-severity finding requiring an exact current exception.
    High,
    /// Critical finding requiring an exact current exception.
    Critical,
}

/// One scanner finding bound to the exact sign-off payload.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VulnerabilityFinding {
    /// Stable advisory or scanner finding identity.
    pub finding_id: FindingId,
    /// Affected package.
    pub package: NonEmptyText,
    /// Installed package version.
    pub installed_version: NonEmptyText,
    /// Canonical severity.
    pub severity: VulnerabilitySeverity,
    /// Exact OCI payload inspected.
    pub artifact_payload_digest: Digest,
}

/// Strict Gregorian UTC date with canonical `YYYY-MM-DD` encoding.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct UtcDate(u16, u8, u8);

impl UtcDate {
    /// Parses and validates one fixed UTC calendar date.
    pub fn new(value: &str) -> Result<Self, &'static str> {
        let bytes = value.as_bytes();
        if bytes.len() != 10
            || bytes[4] != b'-'
            || bytes[7] != b'-'
            || !bytes[..4].iter().all(u8::is_ascii_digit)
            || !bytes[5..7].iter().all(u8::is_ascii_digit)
            || !bytes[8..].iter().all(u8::is_ascii_digit)
        {
            return Err("date must use YYYY-MM-DD");
        }
        let year = value[0..4].parse::<u16>().map_err(|_| "invalid year")?;
        let month = value[5..7].parse::<u8>().map_err(|_| "invalid month")?;
        let day = value[8..10].parse::<u8>().map_err(|_| "invalid day")?;
        let leap =
            year.is_multiple_of(4) && (!year.is_multiple_of(100) || year.is_multiple_of(400));
        let maximum = match month {
            1 | 3 | 5 | 7 | 8 | 10 | 12 => 31,
            4 | 6 | 9 | 11 => 30,
            2 if leap => 29,
            2 => 28,
            _ => return Err("invalid month"),
        };
        if day == 0 || day > maximum {
            return Err("invalid day");
        }
        Ok(Self(year, month, day))
    }
}

impl fmt::Display for UtcDate {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{:04}-{:02}-{:02}", self.0, self.1, self.2)
    }
}

impl Serialize for UtcDate {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for UtcDate {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        Self::new(&String::deserialize(deserializer)?).map_err(D::Error::custom)
    }
}

/// Closed machine-evaluable exception expiry condition.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum ExpiryPredicate {
    /// Expires on or after one fixed UTC date.
    FixedDate {
        /// First date on which the exception is stale.
        expires_on: UtcDate,
    },
    /// Expires when sign-off moves away from the reviewed target revision.
    TargetRevision {
        /// Sole Dagger revision for which the exception was reviewed.
        reviewed_revision: CommitSha,
    },
    /// Expires when the package reaches a fixed patched version.
    PatchedVersion {
        /// Package whose fixed version expires the exception.
        package: NonEmptyText,
        /// First version containing the upstream remediation.
        patched_version: SemverVersion,
    },
    /// Expires when the advisory is withdrawn upstream.
    AdvisoryWithdrawal {
        /// Upstream advisory whose withdrawal expires the exception.
        advisory: FindingId,
    },
}

/// One finding-specific reviewed security exception.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SecurityException {
    /// Exact finding covered.
    pub finding_id: FindingId,
    /// Digest of reviewed reachability reasoning.
    pub reachability_digest: Digest,
    /// Digest of reviewed impact reasoning.
    pub impact_digest: Digest,
    /// Stable team or role owner.
    pub owner: ProvenanceId,
    /// Digest of upstream remediation evidence.
    pub upstream_remediation_digest: Digest,
    /// Machine-evaluable expiry condition.
    pub expiry: ExpiryPredicate,
}

/// Current structured facts used to evaluate exception expiry.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExceptionEvaluationContext {
    /// Current UTC policy date.
    pub current_date: UtcDate,
    /// Current exact Dagger target revision.
    pub target_revision: CommitSha,
    /// Current known fixed versions by package.
    pub fixed_versions: BTreeMap<NonEmptyText, SemverVersion>,
    /// Advisories observed as withdrawn.
    pub withdrawn_advisories: CanonicalSet<FindingId>,
}

/// Complete finding admission with all underlying findings retained.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VulnerabilityAdmission {
    /// Exact payload inspected without rebuild.
    pub artifact_payload_digest: Digest,
    /// Reviewed scanner record.
    pub scanner_provenance: ProvenanceId,
    /// Exact database metadata identity.
    pub database_digest: Digest,
    /// Complete canonical finding set.
    pub findings: CanonicalSet<VulnerabilityFinding>,
    /// Exact current exceptions; findings remain retained.
    pub exceptions: CanonicalSet<SecurityException>,
    /// Domain-separated policy identity.
    pub admission_digest: Digest,
}

/// One normalized scanner finding before it is bound to the enclosing payload identity.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ScannerFindingObservation {
    /// Stable advisory or scanner finding identity.
    pub finding_id: FindingId,
    /// Affected operating-system or language package.
    pub package: NonEmptyText,
    /// Version observed in the exact OCI payload.
    pub installed_version: NonEmptyText,
    /// Closed severity; unknown values fail canonical decoding.
    pub severity: VulnerabilitySeverity,
}

/// Canonical bounded observation emitted for one supplied OCI archive.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ArtifactScannerObservation {
    /// Durable scanner-observation format.
    pub format_version: ConformanceFormatVersion,
    /// Direct SHA-256 identity of the existing file passed to the scanner.
    pub payload_digest: Digest,
    /// Reviewed scanner provenance record.
    pub scanner_provenance: ProvenanceId,
    /// Scanner semantic version observed from the running image.
    pub scanner_version: SemverVersion,
    /// Immutable image identity selected by the graph.
    pub scanner_image_digest: Digest,
    /// Reviewed database-source provenance record.
    pub database_provenance: ProvenanceId,
    /// Immutable OCI manifest identity used to fetch the database.
    pub database_artifact_digest: Digest,
    /// Direct identity of the exact database and metadata checksum document.
    pub database_content_digest: Digest,
    /// Identity of the exact database metadata used by this scan.
    pub database_metadata_digest: Digest,
    /// Every scanner finding, before exception policy is evaluated.
    pub findings: Vec<ScannerFindingObservation>,
    /// Digest of the bounded canonical raw scanner result retained for audit.
    pub scanner_result_digest: Digest,
    /// Positive bounded target scan duration.
    pub elapsed: NonZeroMillis,
    /// The exact archive must enter the scanner once.
    pub artifact_input_count: u32,
    /// Rebuilding target content in the scanner graph is forbidden.
    pub target_build_count: u32,
    /// Repository/source scans are outside this exact-artifact function.
    pub source_scan_count: u32,
}

/// Complete artifact-security input joined with already admitted ordinary and canary evidence.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ArtifactSecurityObservation {
    /// Exact scanner output translated from bounded canonical bytes.
    pub scanner: ArtifactScannerObservation,
    /// Current finding-specific exceptions; findings are never removed.
    pub exceptions: Vec<SecurityException>,
    /// Already admitted exhaustive canary and durable-evidence result.
    pub secret_report: SecretEvidenceReport,
    /// Positive bounded Rust policy-evaluation duration.
    pub policy_elapsed: NonZeroMillis,
}

/// Atomic security result bound to the exact reusable artifact.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ArtifactSecurityReport {
    /// Durable report format.
    pub format_version: ConformanceFormatVersion,
    /// Exact canonical artifact manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Exact existing OCI payload identity.
    pub artifact_payload_digest: Digest,
    /// Canonical identity of the sole independently admitted Import receipt.
    pub artifact_import_receipt_digest: Digest,
    /// Ordinary locked dependency/security closure.
    pub rust_security_digest: Digest,
    /// Reviewed external-input registry identity.
    pub provenance_registry_digest: Digest,
    /// Immutable scanner image identity.
    pub scanner_image_digest: Digest,
    /// Immutable OCI manifest identity used to fetch the vulnerability database.
    pub database_artifact_digest: Digest,
    /// Direct identity of the exact database and metadata checksum document.
    pub database_content_digest: Digest,
    /// Exact vulnerability database metadata identity.
    pub database_metadata_digest: Digest,
    /// Exact bounded raw scanner-result identity observed for this payload.
    pub scanner_result_digest: Digest,
    /// All scanner findings and current exceptions.
    pub vulnerability: VulnerabilityAdmission,
    /// Exhaustive canary and redaction report identity.
    pub secret_report_digest: Digest,
    /// Exact inspected secret domains.
    pub inspected_domains: CanonicalSet<SecretInspectionDomain>,
    /// Target scan duration.
    pub scan_elapsed: NonZeroMillis,
    /// Rust policy evaluation duration.
    pub policy_elapsed: NonZeroMillis,
    /// Domain-separated identity of this complete pass result.
    pub report_digest: Digest,
}

/// Credential boundary represented by one independent non-production canary.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum SecretCanaryCategory {
    /// Dagger session credential.
    Session,
    /// Registry credential.
    Registry,
    /// Git credential.
    Git,
    /// General environment credential.
    Environment,
    /// Trace propagation credential.
    Trace,
    /// URL user-information credential.
    Url,
}

/// Durable output domain inspected for secret and identity leaks.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum SecretInspectionDomain {
    /// Authored source files.
    SourceFiles,
    /// Generated and packaged files.
    GeneratedAndPackagedFiles,
    /// OCI artifact entries.
    ArtifactEntries,
    /// Cache and provenance keys.
    CacheAndProvenance,
    /// Standard output and standard error.
    ProcessOutput,
    /// Typed errors and Debug rendering.
    ErrorsAndDebug,
    /// Diagnostics and traces.
    DiagnosticsAndTraces,
    /// Durable reports.
    Reports,
    /// Draft atomic verdict.
    DraftVerdict,
}

/// Closed packaged output shape inspected by the auxiliary exact-run scanner.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PackagedArtifactKind {
    /// One raw executable retained by a build-only example.
    RawExecutable,
    /// One OCI image-layout tar retained by a build-only example.
    OciImageTar,
}

/// Explicit limits enforced while inspecting a packaged example output.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PackagedArtifactScanLimits {
    /// Maximum outer file bytes.
    pub file_bytes: u64,
    /// Maximum total compressed layer bytes.
    pub compressed_bytes: u64,
    /// Maximum total expanded layer bytes.
    pub expanded_bytes: u64,
    /// Maximum expanded bytes for one layer entry.
    pub expanded_file_bytes: u64,
    /// Maximum outer plus layer entry count.
    pub entries: u64,
    /// Maximum repository-relative path bytes.
    pub path_bytes: u64,
    /// Maximum path component depth.
    pub path_depth: u64,
}

impl PackagedArtifactScanLimits {
    fn exact() -> Self {
        Self {
            file_bytes: PACKAGED_ARTIFACT_FILE_LIMIT,
            compressed_bytes: PACKAGED_ARTIFACT_COMPRESSED_LIMIT,
            expanded_bytes: PACKAGED_ARTIFACT_EXPANDED_LIMIT,
            expanded_file_bytes: PACKAGED_ARTIFACT_EXPANDED_FILE_LIMIT,
            entries: PACKAGED_ARTIFACT_ENTRY_LIMIT,
            path_bytes: PACKAGED_ARTIFACT_PATH_LIMIT as u64,
            path_depth: PACKAGED_ARTIFACT_DEPTH_LIMIT as u64,
        }
    }
}

/// Safe durable result of streaming one actual packaged example output.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PackagedArtifactScanReport {
    /// Durable report format.
    pub format_version: ConformanceFormatVersion,
    /// Fixed scanner implementation identity.
    pub scanner_digest: Digest,
    /// Exact isolated-workspace output path.
    pub artifact_path: RepositoryRelativePath,
    /// Closed raw or OCI shape.
    pub kind: PackagedArtifactKind,
    /// Direct SHA-256 identity independently observed by this scanner.
    pub artifact_digest: Digest,
    /// Actual outer file bytes.
    pub file_bytes: u64,
    /// Compressed layer bytes consumed from an OCI tar.
    pub compressed_bytes: u64,
    /// Expanded regular-file and link-target bytes inspected.
    pub expanded_bytes: u64,
    /// Outer plus expanded layer entries validated.
    pub entries: u64,
    /// Exact limits applied to the scan.
    pub limits: PackagedArtifactScanLimits,
    /// Safe leak coordinates; a complete sign-off admits only an empty set.
    pub findings: CanonicalSet<SecretLeakObservation>,
    /// Domain-separated identity over the complete preceding result.
    pub result_digest: Digest,
}

/// Complete fixed set of actual build-only example outputs inspected during sign-off.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PackagedArtifactScanBundle {
    /// Durable bundle format.
    pub format_version: ConformanceFormatVersion,
    /// Exact three packaged output reports.
    pub artifacts: CanonicalSet<PackagedArtifactScanReport>,
    /// Aggregate outer file bytes.
    pub file_bytes: u64,
    /// Aggregate compressed OCI layer bytes.
    pub compressed_bytes: u64,
    /// Aggregate expanded OCI layer bytes.
    pub expanded_bytes: u64,
    /// Aggregate outer plus layer entries.
    pub entries: u64,
    /// Domain-separated identity over every report and aggregate.
    pub bundle_digest: Digest,
}

/// Returns the maximum retained byte count admitted for one exact-run evidence domain.
///
/// The limits are intentionally domain-specific: cache identities and failure text should stay
/// small, while aggregate process output, diagnostics, and scanner reports need bounded headroom
/// for the complete case fan-out. Every value remains at or below the global scanner ceiling.
pub const fn secret_evidence_domain_byte_limit(domain: SecretInspectionDomain) -> u64 {
    match domain {
        SecretInspectionDomain::SourceFiles
        | SecretInspectionDomain::GeneratedAndPackagedFiles
        | SecretInspectionDomain::ArtifactEntries
        | SecretInspectionDomain::ErrorsAndDebug => MEBIBYTE,
        SecretInspectionDomain::CacheAndProvenance => 256 * 1024,
        SecretInspectionDomain::ProcessOutput | SecretInspectionDomain::DiagnosticsAndTraces => {
            4 * MEBIBYTE
        }
        SecretInspectionDomain::DraftVerdict => 8 * MEBIBYTE,
        SecretInspectionDomain::Reports => MAX_INSPECTED_BYTES,
    }
}

/// Ephemeral canaries with no `Debug` or serialization support.
pub struct SecretCanarySet {
    values: BTreeMap<SecretCanaryCategory, Vec<u8>>,
    digest: Digest,
}

impl SecretCanarySet {
    /// Returns the durable set identity without exposing bytes.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }

    /// Lends ephemeral values to a live boundary without creating a serializable representation.
    pub fn visit(&self, mut visitor: impl FnMut(SecretCanaryCategory, &[u8])) {
        for (category, value) in self.iter() {
            visitor(category, value);
        }
    }

    fn iter(&self) -> impl Iterator<Item = (SecretCanaryCategory, &[u8])> {
        self.values
            .iter()
            .map(|(category, value)| (*category, value.as_slice()))
    }
}

/// Safe durable coordinate for a detected canary occurrence.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SecretLeakObservation {
    /// Category only; the matched value is never retained.
    pub category: SecretCanaryCategory,
    /// Inspected domain containing the match.
    pub domain: SecretInspectionDomain,
    /// Safe relative or semantic coordinate.
    pub coordinate: RepositoryRelativePath,
}

/// Bounded result from scanning one domain across arbitrary chunks.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SecretInspectionObservation {
    /// Inspected domain.
    pub domain: SecretInspectionDomain,
    /// Total bytes inspected.
    pub inspected_bytes: u64,
    /// Canonical leak coordinates without matched values.
    pub leaks: CanonicalSet<SecretLeakObservation>,
}

/// Sanitized durable bytes represented only by digest and size.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SanitizedEvidence {
    /// Digest of scanned safe bytes.
    pub digest: Digest,
    /// Bounded encoded size.
    pub byte_count: u64,
}

/// Ephemeral sensitive identity fragments with no serialization or `Debug` support.
pub struct SensitiveIdentitySet(Vec<Vec<u8>>);

impl SensitiveIdentitySet {
    /// Constructs the in-memory deny set, rejecting empty fragments.
    pub fn new(values: impl IntoIterator<Item = Vec<u8>>) -> Result<Self, &'static str> {
        let values = values.into_iter().collect::<Vec<_>>();
        if values.iter().any(Vec::is_empty) {
            return Err("sensitive identity fragments must be non-empty");
        }
        Ok(Self(values))
    }
}

/// Durable secret-safety input; live values are absent by construction.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SecretEvidenceInput {
    /// Digest of the ephemeral canary set.
    pub canary_set_digest: Digest,
    /// One complete observation per required domain.
    pub inspections: Vec<SecretInspectionObservation>,
    /// Sanitized report/artifact/verdict identities.
    pub sanitized_outputs: Vec<SanitizedEvidence>,
    /// Independently admitted actual build-only example outputs.
    pub packaged_artifacts: PackagedArtifactScanBundle,
    /// True only when the exact artifact contains no live credentials.
    pub artifact_credentials_absent: bool,
    /// True only when the verdict contains no live credentials.
    pub verdict_credentials_absent: bool,
    /// True only when every failure source passed redaction proof.
    pub redaction_proven: bool,
}

/// Admitted complete secret and durable-evidence safety result.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SecretEvidenceReport {
    /// Digest of the ephemeral canary set.
    pub canary_set_digest: Digest,
    /// Complete inspected-domain set.
    pub inspected_domains: CanonicalSet<SecretInspectionDomain>,
    /// Sanitized output identities.
    pub sanitized_outputs: CanonicalSet<Digest>,
    /// Complete bounded scan result for the actual packaged example outputs.
    pub packaged_artifacts: PackagedArtifactScanBundle,
    /// Domain-separated report identity.
    pub report_digest: Digest,
}

/// Returns the exact supported Cargo workspace/example roots.
pub fn required_cargo_roots() -> CanonicalSet<SupportedCargoRoot> {
    CanonicalSet::new([
        cargo_root("sdk/rust/Cargo.toml", "sdk/rust/Cargo.lock"),
        cargo_root(
            "sdk/rust/examples/backend/Cargo.toml",
            "sdk/rust/examples/backend/Cargo.lock",
        ),
        cargo_root(
            "sdk/rust/examples/cli/Cargo.toml",
            "sdk/rust/examples/cli/Cargo.lock",
        ),
        cargo_root(
            "sdk/rust/examples/frontend/Cargo.toml",
            "sdk/rust/examples/frontend/Cargo.lock",
        ),
    ])
}

/// Returns every committed lockfile covered by Rust security review.
pub fn required_committed_lockfiles() -> CanonicalSet<RepositoryRelativePath> {
    CanonicalSet::new(
        [
            "sdk/rust/Cargo.lock",
            "sdk/rust/crates/dagger-codegen/Cargo.lock",
            "sdk/rust/examples/backend/Cargo.lock",
            "sdk/rust/examples/cli/Cargo.lock",
            "sdk/rust/examples/frontend/Cargo.lock",
        ]
        .into_iter()
        .map(relative),
    )
}

/// Returns the reviewed ordinary Rust security observation emitted after the external gates pass.
///
/// Repository source-policy tests independently prove that the checked manifests, lockfiles,
/// automation, permissions, unsafe policy, and packaged dependency descriptors still match these
/// facts. This constructor records that closed result without replaying Cargo or network work.
pub fn reviewed_rust_dependency_security_observation() -> RustDependencySecurityObservation {
    let cargo_roots = required_cargo_roots();
    let manifests = CanonicalSet::new(cargo_roots.iter().map(|root| root.manifest.clone()));
    RustDependencySecurityObservation {
        format_version: ConformanceFormatVersion::V1,
        cargo_roots: cargo_roots.into_inner(),
        committed_lockfiles: required_committed_lockfiles(),
        locked_roots: manifests.clone(),
        cargo_deny_classes: CanonicalSet::new([
            CargoDenyClass::Advisories,
            CargoDenyClass::Licenses,
            CargoDenyClass::Bans,
            CargoDenyClass::Sources,
        ]),
        reachable_advisories: CanonicalSet::default(),
        unapproved_licenses: CanonicalSet::default(),
        unapproved_wildcards: CanonicalSet::default(),
        unknown_sources: CanonicalSet::default(),
        workspace_unsafe_denied: true,
        unsafe_exceptions: Vec::new(),
        automated_cargo_roots: manifests,
        inapplicable_automation: CanonicalSet::default(),
        packaged_dependencies_immutable: true,
        workflow_permissions: BTreeMap::from([(
            NonEmptyText::new("contents").expect("reviewed permission scope is valid"),
            WorkflowPermissionLevel::Read,
        )]),
    }
}

/// Admits exact locked roots, all Cargo Deny classes, unsafe policy, automation, and permissions.
pub fn admit_rust_dependency_security(
    observation: RustDependencySecurityObservation,
) -> Result<RustDependencySecurityReport, ConformanceDiagnosticSet> {
    let roots = CanonicalSet::new(observation.cargo_roots.clone());
    let root_manifests = CanonicalSet::new(roots.iter().map(|root| root.manifest.clone()));
    let deny_classes = CanonicalSet::new([
        CargoDenyClass::Advisories,
        CargoDenyClass::Licenses,
        CargoDenyClass::Bans,
        CargoDenyClass::Sources,
    ]);
    let unsafe_valid = observation.unsafe_exceptions.iter().all(|exception| {
        exception.narrow_allow
            && exception.invariant_digest != Digest::sha256([])
            && exception.test_digest != Digest::sha256([])
    });
    let permissions_valid = observation.workflow_permissions.len() == 1
        && observation
            .workflow_permissions
            .iter()
            .all(|(scope, level)| {
                scope.as_str() == "contents" && *level == WorkflowPermissionLevel::Read
            });
    let valid = roots == required_cargo_roots()
        && roots.len() == observation.cargo_roots.len()
        && observation.committed_lockfiles == required_committed_lockfiles()
        && observation.locked_roots == root_manifests
        && observation.cargo_deny_classes == deny_classes
        && observation.reachable_advisories.is_empty()
        && observation.unapproved_licenses.is_empty()
        && observation.unapproved_wildcards.is_empty()
        && observation.unknown_sources.is_empty()
        && observation.workspace_unsafe_denied
        && unsafe_valid
        && observation.automated_cargo_roots == root_manifests
        && observation.inapplicable_automation.is_empty()
        && observation.packaged_dependencies_immutable
        && permissions_valid;
    if !valid {
        return Err(one_security_diagnostic(
            ConformanceDiagnosticCode::RustSecurityGateFailed,
            "locked Rust dependency or automation security policy failed",
            None,
        ));
    }
    let security_digest = canonical_digest(DigestDomain::ConformanceSecurity, &observation)
        .expect("validated Rust security observation is canonically encodable");
    Ok(RustDependencySecurityReport {
        format_version: observation.format_version,
        cargo_roots: roots,
        committed_lockfiles: observation.committed_lockfiles,
        security_digest,
    })
}

/// Returns the exact role set required by external provenance policy.
pub fn required_external_input_roles() -> BTreeSet<ExternalInputRole> {
    use ExternalInputRole as Role;
    [
        Role::ArtifactBuilderImage,
        Role::EngineBaseImage,
        Role::RustToolchain,
        Role::GoToolchain,
        Role::PreflightCli,
        Role::PreflightEngine,
        Role::CliArchive,
        Role::ScannerImage,
        Role::VulnerabilityDatabaseSource,
    ]
    .into_iter()
    .collect()
}

/// Returns the reviewed external inputs checked into the completeness contract.
pub fn reviewed_external_provenance_input() -> ExternalProvenanceRegistryInput {
    use ExternalInputRole as Role;
    let rust_sdk_review = "sha256:49497d39824f5694a8d9dc07583e3ea4a466b5c16a2f111fa63cc0c65f96ed19";
    let preflight_cli_review =
        "sha256:f0277176eaa73b1c46cddc6f0908bda19f876fa1a85fa1d34cb858b730ea0d3f";
    let preflight_engine_review =
        "sha256:9782caf579780b1db1d6f65e07f4829b6203b6ef4250dc3a2f76fb98a85bb681";
    let cli_review = "sha256:ab4f2a0a6cf68228a74281a0d4a9fc192ab933f6a8bc92218b592e48692b655d";
    ExternalProvenanceRegistryInput {
        format_version: ConformanceFormatVersion::V1,
        records: vec![
            provenance_record(
                "image/rust/1.97.1-bookworm",
                Role::ArtifactBuilderImage,
                "docker-official-images",
                "github.com/docker-library/rust",
                "sha256:705e294093973d7c10e83400393dce7b3611f8e03e55a80af7fff6d02ae1affb",
                rust_sdk_review,
            ),
            provenance_record(
                "image/dagger-engine/beta.9",
                Role::EngineBaseImage,
                "dagger",
                "github.com/dagger/dagger",
                "sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e",
                rust_sdk_review,
            ),
            provenance_record(
                "toolchain/rust/1.97.1/8bab26f4f68e0e26f0bb7960be334d5b520ea452",
                Role::RustToolchain,
                "rust-project",
                "github.com/rust-lang/rust",
                "sha256:c60a41fd857ed7cc29207d7733c9816bedd227259cbd2751cf8efc825fa62280",
                "sha256:3fca259635cc3616a3c9f899442a43dc0b39174429eeb3d3ae58ab2a08d27af3",
            ),
            provenance_record(
                "image/golang/1.26.1-bookworm",
                Role::GoToolchain,
                "docker-official-images",
                "github.com/docker-library/golang",
                "sha256:ab3d6955bbc813a0f3fdf220c1d817dd89c0b3f283777db8ece4a32fe7858edd",
                rust_sdk_review,
            ),
            provenance_record(
                "binary/preflight/d40f9c27",
                Role::PreflightCli,
                "dagger-rust-sdk-maintainers",
                "github.com/dagger/dagger",
                "sha256:d40f9c27e780321fcd0aaa59dde74ad0a7b851caf7378d9026df3ea7ed6f5ed6",
                preflight_cli_review,
            ),
            provenance_record(
                "image/preflight-engine/beta.9",
                Role::PreflightEngine,
                "dagger",
                "github.com/dagger/dagger",
                "sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e",
                preflight_engine_review,
            ),
            provenance_record(
                "archive/dagger-cli/beta.9/linux-amd64",
                Role::CliArchive,
                "dagger",
                "github.com/dagger/dagger",
                "sha256:776a390ecef59ff2ad8c0a3b3ca6d793bb62556bb8a512f475a725bdc830e40c",
                cli_review,
            ),
            provenance_record(
                "image/trivy/0.69.3",
                Role::ScannerImage,
                "aqua-security",
                "github.com/aquasecurity/trivy",
                "sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c",
                "sha256:3b3f0d67ca232d3d13a5ee643ce8ef1d7baa647a9e9b2a1da41c5e907388d6a4",
            ),
            provenance_record_with_review_evidence(
                "image/trivy-db/sha256-10a3832219beaf45a3eb86065e30b39e528ae9c1650aa5f733d4666afd0712c5",
                Role::VulnerabilityDatabaseSource,
                "aqua-security",
                "github.com/aquasecurity/trivy-db",
                "sha256:76213b27bda05820231b84c09ca2854ec548147e9b46c0974247116f4ced4f67",
                TRIVY_DATABASE_REVIEW_EVIDENCE,
            ),
        ],
    }
}

/// Compiles one immutable, reviewed, role-compatible record per external role.
pub fn compile_external_provenance_registry(
    input: ExternalProvenanceRegistryInput,
) -> Result<ExternalProvenanceRegistry, ConformanceDiagnosticSet> {
    let mut records = BTreeMap::new();
    let mut valid = true;
    for record in input.records {
        valid &= provenance_role_matches(&record);
        valid &= records.insert(record.role, record).is_none();
    }
    valid &= records.keys().copied().collect::<BTreeSet<_>>() == required_external_input_roles();
    if !valid {
        return Err(one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "external provenance is missing duplicated mutable or role-incompatible",
            None,
        ));
    }
    let registry_digest = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(input.format_version, &records),
    )
    .expect("validated provenance registry is canonically encodable");
    Ok(ExternalProvenanceRegistry {
        format_version: input.format_version,
        records,
        registry_digest,
    })
}

/// Admits exact-payload findings and only current machine-expiring exceptions.
#[allow(clippy::too_many_arguments)]
pub fn admit_vulnerability_findings(
    artifact_payload_digest: Digest,
    scanner_provenance: ProvenanceId,
    database_digest: Digest,
    registry: &ExternalProvenanceRegistry,
    findings: Vec<VulnerabilityFinding>,
    exceptions: Vec<SecurityException>,
    context: &ExceptionEvaluationContext,
    rebuilt_payload: bool,
) -> Result<VulnerabilityAdmission, ConformanceDiagnosticSet> {
    let scanner_valid = registry
        .records
        .get(&ExternalInputRole::ScannerImage)
        .is_some_and(|record| record.id == scanner_provenance);
    let database_valid = registry
        .records
        .get(&ExternalInputRole::VulnerabilityDatabaseSource)
        .is_some_and(|record| record.immutable_digest == database_digest);
    let finding_set = CanonicalSet::new(findings.clone());
    let exception_set = CanonicalSet::new(exceptions.clone());
    let finding_map = findings
        .iter()
        .map(|finding| (&finding.finding_id, finding))
        .collect::<BTreeMap<_, _>>();
    let exception_map = exceptions
        .iter()
        .map(|exception| (&exception.finding_id, exception))
        .collect::<BTreeMap<_, _>>();
    let mut diagnostics = Vec::new();
    if rebuilt_payload
        || !scanner_valid
        || !database_valid
        || database_digest == Digest::sha256([])
        || findings.len() != finding_map.len()
        || exceptions.len() != exception_map.len()
        || findings
            .iter()
            .any(|finding| finding.artifact_payload_digest != artifact_payload_digest)
    {
        diagnostics.push(security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "scanner database or exact payload provenance is invalid",
            None,
        ));
    }
    for exception in &exceptions {
        if !finding_map.contains_key(&exception.finding_id)
            || exception.reachability_digest == Digest::sha256([])
            || exception.impact_digest == Digest::sha256([])
            || exception.upstream_remediation_digest == Digest::sha256([])
            || expiry_is_true(&exception.expiry, context)
        {
            diagnostics.push(security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityExceptionInvalid,
                "security exception is unrelated incomplete or expired",
                Some(exception.finding_id.clone()),
            ));
        }
    }
    for finding in &findings {
        if finding.severity == VulnerabilitySeverity::Unknown {
            diagnostics.push(security_diagnostic(
                ConformanceDiagnosticCode::ArtifactVulnerabilityGateFailed,
                "vulnerability finding severity is unknown",
                Some(finding.finding_id.clone()),
            ));
        }
        if matches!(
            finding.severity,
            VulnerabilitySeverity::High | VulnerabilitySeverity::Critical
        ) && !exception_map.contains_key(&finding.finding_id)
        {
            diagnostics.push(security_diagnostic(
                ConformanceDiagnosticCode::ArtifactVulnerabilityGateFailed,
                "high or critical finding has no current exact exception",
                Some(finding.finding_id.clone()),
            ));
        }
    }
    if let Some(diagnostics) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    let admission_digest = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &artifact_payload_digest,
            &scanner_provenance,
            &database_digest,
            &finding_set,
            &exception_set,
        ),
    )
    .expect("validated finding admission is canonically encodable");
    Ok(VulnerabilityAdmission {
        artifact_payload_digest,
        scanner_provenance,
        database_digest,
        findings: finding_set,
        exceptions: exception_set,
        admission_digest,
    })
}

/// Decodes a canonical scanner observation and binds every finding to its one enclosing payload.
pub fn decode_artifact_scanner_observation(
    bytes: &[u8],
) -> Result<ArtifactScannerObservation, ConformanceDiagnosticSet> {
    crate::canonical::decode_canonical(bytes).map_err(|_| {
        one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scanner observation is malformed or not canonical",
            None,
        )
    })
}

/// Translates bounded Trivy files into the canonical exact-artifact scanner observation.
///
/// The caller supplies the exact payload checksum emitted beside the raw report. Scanner and
/// database provenance are selected from the already admitted registry rather than from tool
/// output, while every reported finding remains visible to later Rust policy admission.
// Each independently bounded evidence file stays explicit at this trust boundary.
#[allow(clippy::too_many_arguments)]
pub fn translate_trivy_artifact_scan(
    findings_json: &[u8],
    scanner_version_json: &[u8],
    database_metadata_json: &[u8],
    database_checksums: &[u8],
    database_artifact_digest: Digest,
    payload_checksum: &str,
    elapsed_millis: u64,
    registry: &ExternalProvenanceRegistry,
) -> Result<ArtifactScannerObservation, ConformanceDiagnosticSet> {
    const MAX_SCANNER_FILE_BYTES: usize = 16 * 1024 * 1024;
    if findings_json.is_empty()
        || findings_json.len() > MAX_SCANNER_FILE_BYTES
        || scanner_version_json.is_empty()
        || scanner_version_json.len() > MAX_SCANNER_FILE_BYTES
        || database_metadata_json.is_empty()
        || database_metadata_json.len() > MAX_SCANNER_FILE_BYTES
        || database_checksums.is_empty()
        || database_checksums.len() > 1024
    {
        return Err(one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scanner files are empty or exceed the bounded format",
            None,
        ));
    }
    let findings_value: serde_json::Value =
        serde_json::from_slice(findings_json).map_err(|_| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner findings are malformed",
                None,
            )
        })?;
    let version_value: serde_json::Value =
        serde_json::from_slice(scanner_version_json).map_err(|_| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner version is malformed",
                None,
            )
        })?;
    let _: serde_json::Value = serde_json::from_slice(database_metadata_json).map_err(|_| {
        one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scanner database metadata is malformed",
            None,
        )
    })?;
    let scanner_version = json_string_field(&version_value, "Version")
        .and_then(|value| SemverVersion::new(value).ok())
        .filter(|version| {
            version == &SemverVersion::new("0.69.3").expect("reviewed Trivy version is valid")
        })
        .ok_or_else(|| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner version differs from reviewed Trivy",
                None,
            )
        })?;
    let payload_digest = payload_checksum
        .split_ascii_whitespace()
        .next()
        .and_then(|value| Digest::new(format!("sha256:{value}")).ok())
        .ok_or_else(|| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner payload checksum is malformed",
                None,
            )
        })?;
    let scanner_record = registry
        .records
        .get(&ExternalInputRole::ScannerImage)
        .ok_or_else(|| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner provenance is absent",
                None,
            )
        })?;
    let database_record = registry
        .records
        .get(&ExternalInputRole::VulnerabilityDatabaseSource)
        .ok_or_else(|| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner database provenance is absent",
                None,
            )
        })?;
    let database_content_digest = validate_trivy_database_checksums(
        database_checksums,
        database_metadata_json,
        database_record,
        &database_artifact_digest,
    )?;
    let mut findings = Vec::new();
    if let Some(results) = findings_value
        .get("Results")
        .and_then(serde_json::Value::as_array)
    {
        for result in results {
            let Some(vulnerabilities) = result
                .get("Vulnerabilities")
                .and_then(serde_json::Value::as_array)
            else {
                continue;
            };
            for vulnerability in vulnerabilities {
                findings.push(translate_trivy_finding(vulnerability)?);
            }
        }
    }
    findings.sort();
    if findings.windows(2).any(|pair| pair[0] == pair[1]) {
        return Err(one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scanner findings contain duplicate exact records",
            None,
        ));
    }
    Ok(ArtifactScannerObservation {
        format_version: ConformanceFormatVersion::V1,
        payload_digest,
        scanner_provenance: scanner_record.id.clone(),
        scanner_version,
        scanner_image_digest: scanner_record.immutable_digest.clone(),
        database_provenance: database_record.id.clone(),
        database_artifact_digest,
        database_content_digest,
        database_metadata_digest: Digest::sha256(database_metadata_json),
        findings,
        scanner_result_digest: Digest::sha256(findings_json),
        elapsed: NonZeroMillis::new(elapsed_millis).map_err(|_| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner elapsed time is zero or unbounded",
                None,
            )
        })?,
        artifact_input_count: 1,
        target_build_count: 0,
        source_scan_count: 0,
    })
}

fn validate_trivy_database_checksums(
    checksums: &[u8],
    metadata: &[u8],
    record: &ProvenanceRecord,
    artifact_digest: &Digest,
) -> Result<Digest, ConformanceDiagnosticSet> {
    let text = std::str::from_utf8(checksums).map_err(|_| {
        one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "Trivy database checksum evidence is not UTF-8",
            None,
        )
    })?;
    let lines = text.split_terminator('\n').collect::<Vec<_>>();
    let parse = |line: &str, name: &str| {
        let (digest, observed_name) = line.split_once("  ")?;
        (observed_name == name && canonical_sha256_hex(digest))
            .then(|| Digest::new(format!("sha256:{digest}")).ok())
            .flatten()
    };
    let database_digest = lines.first().and_then(|line| parse(line, "trivy.db"));
    let metadata_digest = lines.get(1).and_then(|line| parse(line, "metadata.json"));
    let expected_id = format!(
        "image/trivy-db/{}",
        artifact_digest.as_str().replace(':', "-")
    );
    // Trivy metadata records download time and can change when the same immutable database is
    // materialized again. Bind provenance to the database bytes while checking metadata bytes
    // independently, so a volatile timestamp cannot redefine the reviewed database identity.
    let valid = text.ends_with('\n')
        && lines.len() == 2
        && metadata_digest.as_ref() == Some(&Digest::sha256(metadata))
        && record.id.as_str() == expected_id
        && database_digest.as_ref() == Some(&record.immutable_digest);
    if valid {
        return Ok(database_digest.expect("valid checksum evidence includes the database digest"));
    }
    Err(one_security_diagnostic(
        ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
        "Trivy database artifact or actual content differs from reviewed provenance",
        None,
    ))
}

fn translate_trivy_finding(
    value: &serde_json::Value,
) -> Result<ScannerFindingObservation, ConformanceDiagnosticSet> {
    let vulnerability = json_string_field(value, "VulnerabilityID").ok_or_else(|| {
        one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scanner finding omits its vulnerability identity",
            None,
        )
    })?;
    let package = json_string_field(value, "PkgName").ok_or_else(|| {
        one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scanner finding omits its package",
            None,
        )
    })?;
    let installed_version = json_string_field(value, "InstalledVersion").ok_or_else(|| {
        one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scanner finding omits its installed version",
            None,
        )
    })?;
    let severity = match json_string_field(value, "Severity")
        .unwrap_or_default()
        .to_ascii_lowercase()
        .as_str()
    {
        "low" => VulnerabilitySeverity::Low,
        "medium" => VulnerabilitySeverity::Medium,
        "high" => VulnerabilitySeverity::High,
        "critical" => VulnerabilitySeverity::Critical,
        _ => VulnerabilitySeverity::Unknown,
    };
    let normalized = vulnerability.to_ascii_lowercase().replace('_', "-");
    let coordinate = Digest::sha256(format!("{package}\0{installed_version}"));
    let suffix = &coordinate.as_str()["sha256:".len().."sha256:".len() + 16];
    Ok(ScannerFindingObservation {
        finding_id: FindingId::new(format!("{normalized}/{suffix}")).map_err(|_| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner finding identity cannot be normalized safely",
                None,
            )
        })?,
        package: NonEmptyText::new(package).map_err(|_| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner package is empty or unbounded",
                None,
            )
        })?,
        installed_version: NonEmptyText::new(installed_version).map_err(|_| {
            one_security_diagnostic(
                ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
                "artifact scanner installed version is empty or unbounded",
                None,
            )
        })?,
        severity,
    })
}

fn json_string_field<'a>(value: &'a serde_json::Value, name: &str) -> Option<&'a str> {
    value.get(name).and_then(serde_json::Value::as_str)
}

/// Admits exact-file scanner output and joins it with ordinary and canary security closure.
pub fn admit_artifact_security(
    artifact: &AdmittedArtifact,
    rust_security: &RustDependencySecurityReport,
    registry: &ExternalProvenanceRegistry,
    exception_context: &ExceptionEvaluationContext,
    observation: ArtifactSecurityObservation,
) -> Result<ArtifactSecurityReport, ConformanceDiagnosticSet> {
    let artifact_import_receipt_digest = artifact.import_receipt_digest().ok_or_else(|| {
        one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact security requires an independently admitted Import receipt",
            None,
        )
    })?;
    let scanner_record = registry.records.get(&ExternalInputRole::ScannerImage);
    let database_record = registry
        .records
        .get(&ExternalInputRole::VulnerabilityDatabaseSource);
    let scanner_valid = scanner_record.is_some_and(|record| {
        record.id == observation.scanner.scanner_provenance
            && record.immutable_digest == observation.scanner.scanner_image_digest
    });
    let database_valid = database_record.is_some_and(|record| {
        record.id == observation.scanner.database_provenance
            && record.immutable_digest == observation.scanner.database_content_digest
            && record.id.as_str()
                == format!(
                    "image/trivy-db/{}",
                    observation
                        .scanner
                        .database_artifact_digest
                        .as_str()
                        .replace(':', "-")
                )
    });
    let input_valid = observation.scanner.format_version == ConformanceFormatVersion::V1
        && &observation.scanner.payload_digest == artifact.payload_digest()
        && observation.scanner.scanner_version
            == SemverVersion::new("0.69.3").expect("reviewed Trivy version is valid")
        && scanner_valid
        && database_valid
        && observation.scanner.database_metadata_digest != Digest::sha256([])
        && observation.scanner.scanner_result_digest != Digest::sha256([])
        && observation.scanner.artifact_input_count == 1
        && observation.scanner.target_build_count == 0
        && observation.scanner.source_scan_count == 0;
    if !input_valid {
        return Err(one_security_diagnostic(
            ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
            "artifact scan is stale rebuilt duplicated or provenance mismatched",
            None,
        ));
    }
    let findings = observation
        .scanner
        .findings
        .iter()
        .map(|finding| VulnerabilityFinding {
            finding_id: finding.finding_id.clone(),
            package: finding.package.clone(),
            installed_version: finding.installed_version.clone(),
            severity: finding.severity,
            artifact_payload_digest: artifact.payload_digest().clone(),
        })
        .collect();
    let vulnerability = admit_vulnerability_findings(
        artifact.payload_digest().clone(),
        observation.scanner.scanner_provenance.clone(),
        observation.scanner.database_content_digest.clone(),
        registry,
        findings,
        observation.exceptions,
        exception_context,
        false,
    )?;
    let report_input = (
        artifact.manifest_digest().clone(),
        artifact.payload_digest().clone(),
        artifact_import_receipt_digest.clone(),
        rust_security.security_digest.clone(),
        registry.registry_digest.clone(),
        observation.scanner.scanner_image_digest.clone(),
        observation.scanner.database_artifact_digest.clone(),
        observation.scanner.database_content_digest.clone(),
        observation.scanner.database_metadata_digest.clone(),
        observation.scanner.scanner_result_digest.clone(),
        vulnerability.clone(),
        observation.secret_report.report_digest.clone(),
        observation.secret_report.inspected_domains.clone(),
        observation.scanner.elapsed,
        observation.policy_elapsed,
    );
    let report_digest = canonical_digest(DigestDomain::ConformanceSecurity, &report_input)
        .expect("validated artifact security report is canonically encodable");
    Ok(ArtifactSecurityReport {
        format_version: ConformanceFormatVersion::V1,
        artifact_manifest_digest: artifact.manifest_digest().clone(),
        artifact_payload_digest: artifact.payload_digest().clone(),
        artifact_import_receipt_digest: artifact_import_receipt_digest.clone(),
        rust_security_digest: rust_security.security_digest.clone(),
        provenance_registry_digest: registry.registry_digest.clone(),
        scanner_image_digest: observation.scanner.scanner_image_digest,
        database_artifact_digest: observation.scanner.database_artifact_digest,
        database_content_digest: observation.scanner.database_content_digest,
        database_metadata_digest: observation.scanner.database_metadata_digest,
        scanner_result_digest: observation.scanner.scanner_result_digest,
        vulnerability,
        secret_report_digest: observation.secret_report.report_digest,
        inspected_domains: observation.secret_report.inspected_domains,
        scan_elapsed: observation.scanner.elapsed,
        policy_elapsed: observation.policy_elapsed,
        report_digest,
    })
}

/// Revalidates every embedded identity in a persisted artifact-security pass report.
///
/// The report deliberately contains no raw credentials or scanner output. Revalidation therefore
/// proves the complete retained identity graph while the separately retained scanner result and
/// canary inspections remain responsible for their original observations.
pub fn validate_artifact_security_report(
    report: &ArtifactSecurityReport,
) -> Result<(), ConformanceDiagnosticSet> {
    let vulnerability = &report.vulnerability;
    let expected_vulnerability = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &vulnerability.artifact_payload_digest,
            &vulnerability.scanner_provenance,
            &vulnerability.database_digest,
            &vulnerability.findings,
            &vulnerability.exceptions,
        ),
    );
    let expected_report = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &report.artifact_manifest_digest,
            &report.artifact_payload_digest,
            &report.artifact_import_receipt_digest,
            &report.rust_security_digest,
            &report.provenance_registry_digest,
            &report.scanner_image_digest,
            &report.database_artifact_digest,
            &report.database_content_digest,
            &report.database_metadata_digest,
            &report.scanner_result_digest,
            vulnerability,
            &report.secret_report_digest,
            &report.inspected_domains,
            report.scan_elapsed,
            report.policy_elapsed,
        ),
    );
    let self_consistent = report.format_version == ConformanceFormatVersion::V1
        && vulnerability.artifact_payload_digest == report.artifact_payload_digest
        && report.artifact_import_receipt_digest != Digest::sha256([])
        && vulnerability.database_digest == report.database_content_digest
        && report.database_artifact_digest != Digest::sha256([])
        && report.database_metadata_digest != Digest::sha256([])
        && report.scanner_result_digest != Digest::sha256([])
        && expected_vulnerability.is_ok_and(|digest| digest == vulnerability.admission_digest)
        && expected_report.is_ok_and(|digest| digest == report.report_digest);
    if self_consistent {
        return Ok(());
    }
    Err(one_security_diagnostic(
        ConformanceDiagnosticCode::ArtifactSecurityProvenanceInvalid,
        "persisted artifact security report has a stale or inconsistent identity",
        None,
    ))
}

/// Generates six independent non-production canaries from operating-system entropy.
pub fn generate_secret_canary_set() -> Result<SecretCanarySet, &'static str> {
    let mut entropy = [0_u8; 32];
    OsRng
        .try_fill_bytes(&mut entropy)
        .map_err(|_| "operating-system entropy is unavailable")?;
    secret_canary_set_from_entropy(&entropy)
}

/// Derives canaries from caller-owned high-entropy bytes for deterministic fixture replay.
pub fn secret_canary_set_from_entropy(entropy: &[u8]) -> Result<SecretCanarySet, &'static str> {
    if entropy.len() < 32 || entropy.iter().copied().collect::<BTreeSet<_>>().len() < 16 {
        return Err("canary seed does not meet the minimum entropy shape");
    }
    let categories = [
        SecretCanaryCategory::Session,
        SecretCanaryCategory::Registry,
        SecretCanaryCategory::Git,
        SecretCanaryCategory::Environment,
        SecretCanaryCategory::Trace,
        SecretCanaryCategory::Url,
    ];
    let mut values = BTreeMap::new();
    let mut digest_input = Vec::new();
    for category in categories {
        let mut hasher = Sha256::new();
        hasher.update(b"dagger-rust-sdk-non-production-canary-v1\0");
        hasher.update(format!("{category:?}").as_bytes());
        hasher.update(entropy);
        let value = format!("dagger-canary-{}", lower_hex(&hasher.finalize())).into_bytes();
        digest_input.extend_from_slice(&Sha256::digest(&value));
        values.insert(category, value);
    }
    Ok(SecretCanarySet {
        values,
        digest: Digest::sha256(digest_input),
    })
}

/// Scans one complete domain with a rolling tail so chunk splits cannot hide a match.
pub fn inspect_canary_chunks<'a>(
    canaries: &SecretCanarySet,
    domain: SecretInspectionDomain,
    coordinate: RepositoryRelativePath,
    chunks: impl IntoIterator<Item = &'a [u8]>,
) -> Result<SecretInspectionObservation, ConformanceDiagnosticSet> {
    let byte_limit = secret_evidence_domain_byte_limit(domain);
    let maximum = canaries
        .iter()
        .map(|(_, value)| value.len())
        .max()
        .unwrap_or(0);
    let mut tail = Vec::new();
    let mut inspected_bytes = 0_u64;
    let mut leaks = BTreeSet::new();
    for chunk in chunks {
        inspected_bytes = inspected_bytes.saturating_add(chunk.len() as u64);
        if inspected_bytes > byte_limit {
            return Err(one_security_diagnostic(
                ConformanceDiagnosticCode::EvidenceRedactionFailed,
                "inspected evidence exceeded the declared size bound",
                None,
            ));
        }
        let mut window = Vec::with_capacity(tail.len() + chunk.len());
        window.extend_from_slice(&tail);
        window.extend_from_slice(chunk);
        for (category, canary) in canaries.iter() {
            if contains_bytes(&window, canary) {
                leaks.insert(SecretLeakObservation {
                    category,
                    domain,
                    coordinate: coordinate.clone(),
                });
            }
        }
        let retain = maximum.saturating_sub(1).min(window.len());
        tail.clear();
        tail.extend_from_slice(&window[window.len() - retain..]);
    }
    Ok(SecretInspectionObservation {
        domain,
        inspected_bytes,
        leaks: CanonicalSet::new(leaks),
    })
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct OciLayout {
    #[serde(rename = "imageLayoutVersion")]
    image_layout_version: String,
}

#[derive(Deserialize)]
struct OciIndex {
    #[serde(rename = "schemaVersion")]
    schema_version: u32,
    manifests: Vec<OciDescriptor>,
}

#[derive(Deserialize)]
struct OciManifest {
    #[serde(rename = "schemaVersion")]
    schema_version: u32,
    config: OciDescriptor,
    layers: Vec<OciDescriptor>,
}

#[derive(Deserialize)]
struct OciDescriptor {
    #[serde(rename = "mediaType")]
    media_type: String,
    digest: String,
    size: u64,
}

#[derive(Clone)]
struct OciBlobObservation {
    size: u64,
}

struct CanaryStream<'a> {
    canaries: &'a SecretCanarySet,
    domain: SecretInspectionDomain,
    coordinate: RepositoryRelativePath,
    tail: Vec<u8>,
    findings: BTreeSet<SecretLeakObservation>,
}

impl<'a> CanaryStream<'a> {
    fn new(canaries: &'a SecretCanarySet, coordinate: RepositoryRelativePath) -> Self {
        Self {
            canaries,
            domain: SecretInspectionDomain::GeneratedAndPackagedFiles,
            coordinate,
            tail: Vec::new(),
            findings: BTreeSet::new(),
        }
    }

    fn scan(&mut self, chunk: &[u8]) {
        let maximum = self
            .canaries
            .iter()
            .map(|(_, value)| value.len())
            .max()
            .unwrap_or(0);
        let mut window = Vec::with_capacity(self.tail.len() + chunk.len());
        window.extend_from_slice(&self.tail);
        window.extend_from_slice(chunk);
        for (category, canary) in self.canaries.iter() {
            if contains_bytes(&window, canary) {
                self.findings.insert(SecretLeakObservation {
                    category,
                    domain: self.domain,
                    coordinate: self.coordinate.clone(),
                });
            }
        }
        let retain = maximum.saturating_sub(1).min(window.len());
        self.tail.clear();
        self.tail
            .extend_from_slice(&window[window.len().saturating_sub(retain)..]);
    }
}

struct BoundedCountingReader<R> {
    inner: R,
    count: u64,
    limit: u64,
}

impl<R> BoundedCountingReader<R> {
    fn new(inner: R, limit: u64) -> Self {
        Self {
            inner,
            count: 0,
            limit,
        }
    }
}

impl<R: Read> Read for BoundedCountingReader<R> {
    fn read(&mut self, buffer: &mut [u8]) -> std::io::Result<usize> {
        let read = self.inner.read(buffer)?;
        self.count = self.count.saturating_add(read as u64);
        if self.count > self.limit {
            return Err(std::io::Error::other(
                "expanded packaged artifact exceeds its bound",
            ));
        }
        Ok(read)
    }
}

/// Streams one actual raw executable or OCI image tar through the bounded canary scanner.
///
/// OCI blobs are digest-checked before their manifest is trusted. Layer archives are then read
/// without extraction, links are never followed, and compressed plus expanded limits are
/// enforced independently so an archive cannot convert a small input into unbounded work.
pub fn scan_packaged_artifact<R: Read + Seek>(
    reader: &mut R,
    artifact_path: RepositoryRelativePath,
    kind: PackagedArtifactKind,
    expected_digest: &Digest,
    canaries: &SecretCanarySet,
) -> Result<PackagedArtifactScanReport, ConformanceDiagnosticSet> {
    let limits = PackagedArtifactScanLimits::exact();
    let (artifact_digest, file_bytes) = stream_artifact_identity(reader, limits.file_bytes)?;
    if &artifact_digest != expected_digest {
        return Err(packaged_artifact_error(
            "packaged artifact bytes differ from the independently observed identity",
        ));
    }
    reader
        .seek(SeekFrom::Start(0))
        .map_err(|_| packaged_artifact_error("packaged artifact cannot be rewound"))?;

    let (compressed_bytes, expanded_bytes, entries, findings) = match kind {
        PackagedArtifactKind::RawExecutable => {
            let mut scanner = CanaryStream::new(canaries, artifact_path.clone());
            scan_bounded_reader(reader, file_bytes, &mut scanner)?;
            (0, file_bytes, 1, scanner.findings)
        }
        PackagedArtifactKind::OciImageTar => {
            scan_oci_image(reader, &artifact_path, canaries, &limits)?
        }
    };
    let scanner_digest = Digest::sha256(PACKAGED_ARTIFACT_SCANNER_ID);
    let findings = CanonicalSet::new(findings);
    let result_digest = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &scanner_digest,
            &artifact_path,
            kind,
            &artifact_digest,
            file_bytes,
            compressed_bytes,
            expanded_bytes,
            entries,
            &limits,
            &findings,
        ),
    )
    .map_err(|_| packaged_artifact_error("packaged artifact result cannot be encoded"))?;
    Ok(PackagedArtifactScanReport {
        format_version: ConformanceFormatVersion::V1,
        scanner_digest,
        artifact_path,
        kind,
        artifact_digest,
        file_bytes,
        compressed_bytes,
        expanded_bytes,
        entries,
        limits,
        findings,
        result_digest,
    })
}

/// Assembles the exact CLI, backend-image, and frontend-image auxiliary scan results.
pub fn assemble_packaged_artifact_scan_bundle(
    reports: impl IntoIterator<Item = PackagedArtifactScanReport>,
) -> Result<PackagedArtifactScanBundle, ConformanceDiagnosticSet> {
    let artifacts = CanonicalSet::new(reports);
    let expected = BTreeMap::from([
        ("build/cli", PackagedArtifactKind::RawExecutable),
        ("build/backend-image.tar", PackagedArtifactKind::OciImageTar),
        (
            "build/frontend-image.tar",
            PackagedArtifactKind::OciImageTar,
        ),
    ]);
    if artifacts.len() != expected.len()
        || artifacts.iter().any(|report| {
            expected.get(report.artifact_path.as_str()) != Some(&report.kind)
                || report.format_version != ConformanceFormatVersion::V1
                || report.scanner_digest != Digest::sha256(PACKAGED_ARTIFACT_SCANNER_ID)
                || report.limits != PackagedArtifactScanLimits::exact()
                || !report.findings.is_empty()
                || packaged_artifact_result_digest(report).as_ref() != Ok(&report.result_digest)
        })
    {
        return Err(packaged_artifact_error(
            "packaged artifact scan set is incomplete stale or contains findings",
        ));
    }
    let mut file_bytes = 0_u64;
    let mut compressed_bytes = 0_u64;
    let mut expanded_bytes = 0_u64;
    let mut entries = 0_u64;
    for report in artifacts.iter() {
        file_bytes = file_bytes.saturating_add(report.file_bytes);
        compressed_bytes = compressed_bytes.saturating_add(report.compressed_bytes);
        expanded_bytes = expanded_bytes.saturating_add(report.expanded_bytes);
        entries = entries.saturating_add(report.entries);
    }
    if file_bytes > 3 * PACKAGED_ARTIFACT_FILE_LIMIT
        || compressed_bytes > PACKAGED_ARTIFACT_COMPRESSED_LIMIT
        || expanded_bytes > 2 * PACKAGED_ARTIFACT_EXPANDED_LIMIT + PACKAGED_ARTIFACT_FILE_LIMIT
        || entries > 2 * PACKAGED_ARTIFACT_ENTRY_LIMIT + 1
    {
        return Err(packaged_artifact_error(
            "packaged artifact scan set exceeds its aggregate bound",
        ));
    }
    let bundle_digest = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &artifacts,
            file_bytes,
            compressed_bytes,
            expanded_bytes,
            entries,
        ),
    )
    .map_err(|_| packaged_artifact_error("packaged artifact scan set cannot be encoded"))?;
    Ok(PackagedArtifactScanBundle {
        format_version: ConformanceFormatVersion::V1,
        artifacts,
        file_bytes,
        compressed_bytes,
        expanded_bytes,
        entries,
        bundle_digest,
    })
}

fn packaged_artifact_result_digest(
    report: &PackagedArtifactScanReport,
) -> Result<Digest, ConformanceDiagnosticSet> {
    canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &report.scanner_digest,
            &report.artifact_path,
            report.kind,
            &report.artifact_digest,
            report.file_bytes,
            report.compressed_bytes,
            report.expanded_bytes,
            report.entries,
            &report.limits,
            &report.findings,
        ),
    )
    .map_err(|_| packaged_artifact_error("packaged artifact result cannot be encoded"))
}

fn stream_artifact_identity<R: Read + Seek>(
    reader: &mut R,
    limit: u64,
) -> Result<(Digest, u64), ConformanceDiagnosticSet> {
    reader
        .seek(SeekFrom::Start(0))
        .map_err(|_| packaged_artifact_error("packaged artifact cannot be read"))?;
    let mut hasher = Sha256::new();
    let mut total = 0_u64;
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        let read = reader
            .read(&mut buffer)
            .map_err(|_| packaged_artifact_error("packaged artifact cannot be read"))?;
        if read == 0 {
            break;
        }
        total = total.saturating_add(read as u64);
        if total > limit {
            return Err(packaged_artifact_error(
                "packaged artifact exceeds its outer byte bound",
            ));
        }
        hasher.update(&buffer[..read]);
    }
    if total == 0 {
        return Err(packaged_artifact_error("packaged artifact is empty"));
    }
    Ok((Digest::from_sha256_output(hasher.finalize().into()), total))
}

fn scan_oci_image<R: Read + Seek>(
    reader: &mut R,
    artifact_path: &RepositoryRelativePath,
    canaries: &SecretCanarySet,
    limits: &PackagedArtifactScanLimits,
) -> Result<(u64, u64, u64, BTreeSet<SecretLeakObservation>), ConformanceDiagnosticSet> {
    let mut blobs = BTreeMap::<String, OciBlobObservation>::new();
    let mut index = None;
    let mut layout = None;
    let mut outer_entries = 0_u64;
    {
        let mut archive = tar::Archive::new(&mut *reader);
        let entries = archive
            .entries()
            .map_err(|_| packaged_artifact_error("OCI outer archive is malformed"))?;
        for entry in entries {
            let mut entry = entry
                .map_err(|_| packaged_artifact_error("OCI outer archive entry is malformed"))?;
            outer_entries = outer_entries.saturating_add(1);
            if outer_entries > limits.entries {
                return Err(packaged_artifact_error(
                    "OCI outer archive exceeds its entry bound",
                ));
            }
            let path =
                safe_archive_path(&entry.path().map_err(|_| {
                    packaged_artifact_error("OCI outer archive path is malformed")
                })?)?;
            let entry_type = entry.header().entry_type();
            if entry_type.is_dir() {
                continue;
            }
            if !entry_type.is_file() {
                return Err(packaged_artifact_error(
                    "OCI outer archive contains a link or unsupported entry",
                ));
            }
            let size = entry
                .header()
                .size()
                .map_err(|_| packaged_artifact_error("OCI outer entry size is malformed"))?;
            if size > limits.file_bytes {
                return Err(packaged_artifact_error(
                    "OCI outer entry exceeds its file bound",
                ));
            }
            let capture = path == "index.json" || path == "oci-layout";
            let (digest, bytes) = read_entry_identity(&mut entry, size, capture)?;
            if path == "index.json" {
                index = bytes;
            } else if path == "oci-layout" {
                layout = bytes;
            } else if let Some(encoded) = path.strip_prefix("blobs/sha256/") {
                if !canonical_sha256_hex(encoded) || digest.as_str() != format!("sha256:{encoded}")
                {
                    return Err(packaged_artifact_error(
                        "OCI blob path differs from its actual bytes",
                    ));
                }
                if blobs
                    .insert(format!("sha256:{encoded}"), OciBlobObservation { size })
                    .is_some()
                {
                    return Err(packaged_artifact_error("OCI blob is duplicated"));
                }
            } else {
                return Err(packaged_artifact_error(
                    "OCI outer archive contains an unsupported path",
                ));
            }
        }
    }
    let layout_bytes =
        layout.ok_or_else(|| packaged_artifact_error("OCI layout is absent or malformed"))?;
    let layout: OciLayout = serde_json::from_slice(&layout_bytes)
        .map_err(|_| packaged_artifact_error("OCI layout is absent or malformed"))?;
    if layout.image_layout_version != "1.0.0" {
        return Err(packaged_artifact_error("OCI layout version is unsupported"));
    }
    let index_bytes =
        index.ok_or_else(|| packaged_artifact_error("OCI index is absent or malformed"))?;
    let index: OciIndex = serde_json::from_slice(&index_bytes)
        .map_err(|_| packaged_artifact_error("OCI index is absent or malformed"))?;
    if index.schema_version != 2 || index.manifests.len() != 1 {
        return Err(packaged_artifact_error(
            "OCI index must select exactly one image manifest",
        ));
    }
    let manifest_descriptor = &index.manifests[0];
    validate_oci_descriptor(manifest_descriptor, &blobs)?;
    if manifest_descriptor.media_type != "application/vnd.oci.image.manifest.v1+json" {
        return Err(packaged_artifact_error(
            "OCI manifest media type is unsupported",
        ));
    }
    reader
        .seek(SeekFrom::Start(0))
        .map_err(|_| packaged_artifact_error("OCI archive cannot be rewound"))?;
    let manifest_bytes = read_outer_blob(reader, &manifest_descriptor.digest)?;
    let manifest: OciManifest = serde_json::from_slice(&manifest_bytes)
        .map_err(|_| packaged_artifact_error("OCI manifest is malformed"))?;
    if manifest.schema_version != 2 || manifest.layers.is_empty() {
        return Err(packaged_artifact_error("OCI manifest has no layers"));
    }
    validate_oci_descriptor(&manifest.config, &blobs)?;
    if manifest.config.media_type != "application/vnd.oci.image.config.v1+json" {
        return Err(packaged_artifact_error(
            "OCI config media type is unsupported",
        ));
    }
    let config_bytes = read_outer_blob(reader, &manifest.config.digest)?;
    serde_json::from_slice::<serde_json::Value>(&config_bytes)
        .map_err(|_| packaged_artifact_error("OCI config is malformed"))?;
    let expected_blobs = std::iter::once(manifest_descriptor.digest.as_str())
        .chain(std::iter::once(manifest.config.digest.as_str()))
        .chain(manifest.layers.iter().map(|layer| layer.digest.as_str()))
        .collect::<BTreeSet<_>>();
    if blobs.keys().map(String::as_str).collect::<BTreeSet<_>>() != expected_blobs {
        return Err(packaged_artifact_error(
            "OCI archive contains an unreferenced or missing blob",
        ));
    }
    let mut compressed_bytes = 0_u64;
    for layer in &manifest.layers {
        validate_oci_descriptor(layer, &blobs)?;
        compressed_bytes = compressed_bytes.saturating_add(layer.size);
        if compressed_bytes > limits.compressed_bytes {
            return Err(packaged_artifact_error(
                "OCI layers exceed their compressed aggregate bound",
            ));
        }
    }

    reader
        .seek(SeekFrom::Start(0))
        .map_err(|_| packaged_artifact_error("OCI archive cannot be rewound"))?;
    let mut expanded_bytes = 0_u64;
    let mut layer_entries = 0_u64;
    let mut findings = BTreeSet::new();
    for (coordinate, bytes) in [
        ("oci-layout".to_owned(), layout_bytes.as_slice()),
        ("index.json".to_owned(), index_bytes.as_slice()),
        (
            format!(
                "blobs/sha256/{}",
                manifest_descriptor.digest.trim_start_matches("sha256:")
            ),
            manifest_bytes.as_slice(),
        ),
        (
            format!(
                "blobs/sha256/{}",
                manifest.config.digest.trim_start_matches("sha256:")
            ),
            config_bytes.as_slice(),
        ),
    ] {
        expanded_bytes = expanded_bytes.saturating_add(bytes.len() as u64);
        if expanded_bytes > limits.expanded_bytes {
            return Err(packaged_artifact_error(
                "OCI metadata exceeds its expanded aggregate bound",
            ));
        }
        let coordinate =
            RepositoryRelativePath::new(format!("{}/{}", artifact_path.as_str(), coordinate))
                .map_err(|_| packaged_artifact_error("OCI metadata coordinate is unsafe"))?;
        let mut scanner = CanaryStream::new(canaries, coordinate);
        scanner.scan(bytes);
        findings.extend(scanner.findings);
    }
    let layers = manifest
        .layers
        .iter()
        .map(|layer| (layer.digest.as_str(), layer))
        .collect::<BTreeMap<_, _>>();
    let mut seen = BTreeSet::new();
    let mut archive = tar::Archive::new(&mut *reader);
    for entry in archive
        .entries()
        .map_err(|_| packaged_artifact_error("OCI outer archive is malformed"))?
    {
        let entry =
            entry.map_err(|_| packaged_artifact_error("OCI outer archive entry is malformed"))?;
        let path = safe_archive_path(
            &entry
                .path()
                .map_err(|_| packaged_artifact_error("OCI outer archive path is malformed"))?,
        )?;
        let Some(encoded) = path.strip_prefix("blobs/sha256/") else {
            continue;
        };
        let digest = format!("sha256:{encoded}");
        let Some(layer) = layers.get(digest.as_str()) else {
            continue;
        };
        if !seen.insert(digest.clone()) {
            return Err(packaged_artifact_error("OCI layer blob is duplicated"));
        }
        let coordinate_prefix = format!("{}/{}", artifact_path.as_str(), encoded);
        let (expanded, entries, layer_findings) = match layer.media_type.as_str() {
            "application/vnd.oci.image.layer.v1.tar" => scan_layer_tar(
                entry,
                &coordinate_prefix,
                canaries,
                limits,
                expanded_bytes,
                outer_entries + layer_entries,
            )?,
            "application/vnd.oci.image.layer.v1.tar+gzip" => scan_layer_tar(
                GzDecoder::new(entry),
                &coordinate_prefix,
                canaries,
                limits,
                expanded_bytes,
                outer_entries + layer_entries,
            )?,
            _ => {
                return Err(packaged_artifact_error(
                    "OCI layer compression or media type is unsupported",
                ));
            }
        };
        expanded_bytes = expanded_bytes.saturating_add(expanded);
        layer_entries = layer_entries.saturating_add(entries);
        findings.extend(layer_findings);
    }
    if seen.len() != manifest.layers.len() {
        return Err(packaged_artifact_error("OCI manifest layer blob is absent"));
    }
    Ok((
        compressed_bytes,
        expanded_bytes,
        outer_entries + layer_entries,
        findings,
    ))
}

fn scan_layer_tar<R: Read>(
    reader: R,
    coordinate_prefix: &str,
    canaries: &SecretCanarySet,
    limits: &PackagedArtifactScanLimits,
    prior_expanded: u64,
    prior_entries: u64,
) -> Result<(u64, u64, BTreeSet<SecretLeakObservation>), ConformanceDiagnosticSet> {
    let remaining = limits.expanded_bytes.saturating_sub(prior_expanded);
    let mut counted = BoundedCountingReader::new(reader, remaining);
    let mut entries_seen = 0_u64;
    let mut findings = BTreeSet::new();
    {
        let mut archive = tar::Archive::new(&mut counted);
        for entry in archive
            .entries()
            .map_err(|_| packaged_artifact_error("OCI layer archive is malformed"))?
        {
            let mut entry =
                entry.map_err(|_| packaged_artifact_error("OCI layer entry is malformed"))?;
            entries_seen = entries_seen.saturating_add(1);
            if prior_entries.saturating_add(entries_seen) > limits.entries {
                return Err(packaged_artifact_error(
                    "OCI layer archives exceed their entry bound",
                ));
            }
            let path = safe_archive_path(
                &entry
                    .path()
                    .map_err(|_| packaged_artifact_error("OCI layer path is malformed"))?,
            )?;
            let coordinate = RepositoryRelativePath::new(format!(
                "{coordinate_prefix}/{}",
                path.trim_start_matches("./")
            ))
            .map_err(|_| packaged_artifact_error("OCI layer coordinate is unsafe"))?;
            let entry_type = entry.header().entry_type();
            if entry_type.is_dir() {
                continue;
            }
            if entry_type.is_file() {
                let size = entry
                    .header()
                    .size()
                    .map_err(|_| packaged_artifact_error("OCI expanded entry size is malformed"))?;
                if size > limits.expanded_file_bytes {
                    return Err(packaged_artifact_error(
                        "OCI expanded entry exceeds its per-file bound",
                    ));
                }
                let mut scanner = CanaryStream::new(canaries, coordinate);
                scan_bounded_reader(&mut entry, size, &mut scanner)?;
                findings.extend(scanner.findings);
                continue;
            }
            if entry_type.is_symlink() {
                let target = entry
                    .link_name()
                    .map_err(|_| packaged_artifact_error("OCI symlink target is malformed"))?
                    .ok_or_else(|| packaged_artifact_error("OCI symlink target is absent"))?;
                let target = target.as_os_str().as_encoded_bytes();
                if target.is_empty() || target.len() > PACKAGED_ARTIFACT_PATH_LIMIT {
                    return Err(packaged_artifact_error(
                        "OCI symlink target exceeds its bound",
                    ));
                }
                let mut scanner = CanaryStream::new(canaries, coordinate);
                scanner.scan(target);
                findings.extend(scanner.findings);
                continue;
            }
            return Err(packaged_artifact_error(
                "OCI layer contains a hard link or unsupported entry",
            ));
        }
    }
    Ok((counted.count, entries_seen, findings))
}

fn scan_bounded_reader<R: Read>(
    reader: &mut R,
    exact_size: u64,
    scanner: &mut CanaryStream<'_>,
) -> Result<(), ConformanceDiagnosticSet> {
    let mut total = 0_u64;
    let mut buffer = [0_u8; 64 * 1024];
    while total < exact_size {
        let remaining =
            usize::try_from((exact_size - total).min(buffer.len() as u64)).unwrap_or(buffer.len());
        let read = reader
            .read(&mut buffer[..remaining])
            .map_err(|_| packaged_artifact_error("packaged artifact content cannot be read"))?;
        if read == 0 {
            return Err(packaged_artifact_error(
                "packaged artifact content ended before its declared size",
            ));
        }
        total += read as u64;
        scanner.scan(&buffer[..read]);
    }
    Ok(())
}

fn read_entry_identity<R: Read>(
    reader: &mut R,
    exact_size: u64,
    capture: bool,
) -> Result<(Digest, Option<Vec<u8>>), ConformanceDiagnosticSet> {
    if capture && exact_size > PACKAGED_ARTIFACT_METADATA_LIMIT {
        return Err(packaged_artifact_error(
            "OCI metadata exceeds its byte bound",
        ));
    }
    let mut hasher = Sha256::new();
    let mut captured = capture.then(|| Vec::with_capacity(exact_size as usize));
    let mut total = 0_u64;
    let mut buffer = [0_u8; 64 * 1024];
    while total < exact_size {
        let remaining =
            usize::try_from((exact_size - total).min(buffer.len() as u64)).unwrap_or(buffer.len());
        let read = reader
            .read(&mut buffer[..remaining])
            .map_err(|_| packaged_artifact_error("OCI entry cannot be read"))?;
        if read == 0 {
            return Err(packaged_artifact_error(
                "OCI entry ended before its declared size",
            ));
        }
        total += read as u64;
        hasher.update(&buffer[..read]);
        if let Some(captured) = captured.as_mut() {
            captured.extend_from_slice(&buffer[..read]);
        }
    }
    Ok((
        Digest::from_sha256_output(hasher.finalize().into()),
        captured,
    ))
}

fn read_outer_blob<R: Read + Seek>(
    reader: &mut R,
    digest: &str,
) -> Result<Vec<u8>, ConformanceDiagnosticSet> {
    let expected_path = digest
        .strip_prefix("sha256:")
        .filter(|value| canonical_sha256_hex(value))
        .map(|value| format!("blobs/sha256/{value}"))
        .ok_or_else(|| packaged_artifact_error("OCI descriptor digest is malformed"))?;
    reader
        .seek(SeekFrom::Start(0))
        .map_err(|_| packaged_artifact_error("OCI archive cannot be rewound"))?;
    let mut archive = tar::Archive::new(&mut *reader);
    for entry in archive
        .entries()
        .map_err(|_| packaged_artifact_error("OCI outer archive is malformed"))?
    {
        let mut entry =
            entry.map_err(|_| packaged_artifact_error("OCI outer archive entry is malformed"))?;
        let path = safe_archive_path(
            &entry
                .path()
                .map_err(|_| packaged_artifact_error("OCI outer archive path is malformed"))?,
        )?;
        if path == expected_path {
            let size = entry
                .header()
                .size()
                .map_err(|_| packaged_artifact_error("OCI metadata size is malformed"))?;
            return read_entry_identity(&mut entry, size, true)?
                .1
                .ok_or_else(|| packaged_artifact_error("OCI manifest metadata is unavailable"));
        }
    }
    Err(packaged_artifact_error("OCI manifest blob is absent"))
}

fn validate_oci_descriptor(
    descriptor: &OciDescriptor,
    blobs: &BTreeMap<String, OciBlobObservation>,
) -> Result<(), ConformanceDiagnosticSet> {
    if !descriptor
        .digest
        .strip_prefix("sha256:")
        .is_some_and(canonical_sha256_hex)
        || descriptor.size == 0
        || blobs.get(&descriptor.digest).map(|blob| blob.size) != Some(descriptor.size)
    {
        return Err(packaged_artifact_error(
            "OCI descriptor differs from its validated blob",
        ));
    }
    Ok(())
}

fn safe_archive_path(path: &Path) -> Result<String, ConformanceDiagnosticSet> {
    let text = path
        .to_str()
        .filter(|text| !text.is_empty() && text.len() <= PACKAGED_ARTIFACT_PATH_LIMIT)
        .ok_or_else(|| packaged_artifact_error("archive path is empty non-UTF-8 or oversized"))?;
    let mut depth = 0_usize;
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::Normal(_) => depth += 1,
            Component::ParentDir | Component::RootDir | Component::Prefix(_) => {
                return Err(packaged_artifact_error(
                    "archive path is absolute or traverses its root",
                ));
            }
        }
    }
    if depth == 0 || depth > PACKAGED_ARTIFACT_DEPTH_LIMIT {
        return Err(packaged_artifact_error("archive path depth is invalid"));
    }
    Ok(text.trim_start_matches("./").to_owned())
}

fn canonical_sha256_hex(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn packaged_artifact_error(detail: &'static str) -> ConformanceDiagnosticSet {
    one_security_diagnostic(
        ConformanceDiagnosticCode::EvidenceRedactionFailed,
        detail,
        None,
    )
}

#[cfg(test)]
mod packaged_artifact_tests {
    use std::io::{Cursor, Read as _};

    use super::*;

    fn canaries() -> SecretCanarySet {
        secret_canary_set_from_entropy(&std::array::from_fn::<_, 32, _>(|index| index as u8))
            .unwrap()
    }

    #[test]
    fn packaged_artifact_outer_bound_accepts_exact_and_rejects_plus_one() {
        let mut exact = Cursor::new(vec![0_u8; 8]);
        assert_eq!(stream_artifact_identity(&mut exact, 8).unwrap().1, 8);

        let mut oversized = Cursor::new(vec![0_u8; 9]);
        assert!(stream_artifact_identity(&mut oversized, 8).is_err());
    }

    #[test]
    fn packaged_artifact_rejects_traversal_and_unsafe_depth() {
        assert!(safe_archive_path(Path::new("../credential")).is_err());
        assert!(safe_archive_path(Path::new("/absolute/credential")).is_err());
        let deep = std::iter::repeat_n("entry", PACKAGED_ARTIFACT_DEPTH_LIMIT + 1)
            .collect::<Vec<_>>()
            .join("/");
        assert!(safe_archive_path(Path::new(&deep)).is_err());
    }

    #[test]
    fn packaged_artifact_expansion_reader_rejects_a_bomb() {
        let mut reader = BoundedCountingReader::new(Cursor::new(vec![0_u8; 9]), 8);
        let mut output = Vec::new();
        assert!(reader.read_to_end(&mut output).is_err());
    }

    #[test]
    fn packaged_artifact_canary_match_crosses_stream_chunks() {
        let canaries = canaries();
        let mut value = Vec::new();
        canaries.visit(|category, bytes| {
            if category == SecretCanaryCategory::Session {
                value = bytes.to_vec();
            }
        });
        let coordinate = RepositoryRelativePath::new("build/cli").unwrap();
        let mut scanner = CanaryStream::new(&canaries, coordinate);
        let split = value.len() / 2;
        scanner.scan(&value[..split]);
        assert!(scanner.findings.is_empty());
        scanner.scan(&value[split..]);
        assert_eq!(scanner.findings.len(), 1);
    }

    #[test]
    fn packaged_artifact_rejects_a_substituted_identity() {
        let bytes = b"actual packaged executable".to_vec();
        let mut reader = Cursor::new(bytes);
        let result = scan_packaged_artifact(
            &mut reader,
            RepositoryRelativePath::new("build/cli").unwrap(),
            PackagedArtifactKind::RawExecutable,
            &Digest::sha256("substituted packaged executable"),
            &canaries(),
        );
        assert!(result.is_err());
    }
}

/// Rejects canaries, credentials, host/provider identity, controls, and unbounded bytes.
pub fn sanitize_durable_evidence(
    bytes: &[u8],
    canaries: &SecretCanarySet,
    sensitive_identities: &SensitiveIdentitySet,
) -> Result<SanitizedEvidence, ConformanceDiagnosticSet> {
    let forbidden = bytes.len() as u64 > MAX_INSPECTED_BYTES
        || bytes
            .iter()
            .any(|byte| byte.is_ascii_control() && !matches!(byte, b'\n' | b'\r' | b'\t'))
        || contains_absolute_host_path(bytes)
        || [
            b"/Users/".as_slice(),
            b"/home/".as_slice(),
            b"C:\\Users\\".as_slice(),
            b"file://".as_slice(),
            b"authorization:".as_slice(),
            b"token=".as_slice(),
            b"password=".as_slice(),
        ]
        .into_iter()
        .any(|needle| contains_ascii_case_insensitive(bytes, needle))
        || canaries
            .iter()
            .any(|(_, value)| contains_bytes(bytes, value))
        || sensitive_identities
            .0
            .iter()
            .any(|identity| contains_bytes(bytes, identity));
    if forbidden {
        return Err(one_security_diagnostic(
            ConformanceDiagnosticCode::EvidenceRedactionFailed,
            "durable evidence contains unsafe secret path identity or control bytes",
            None,
        ));
    }
    Ok(SanitizedEvidence {
        digest: Digest::sha256(bytes),
        byte_count: bytes.len() as u64,
    })
}

/// Admits only complete leak-free domains and proven artifact/verdict redaction.
pub fn admit_secret_evidence(
    input: SecretEvidenceInput,
) -> Result<SecretEvidenceReport, ConformanceDiagnosticSet> {
    let rebuilt_packaged_artifacts =
        assemble_packaged_artifact_scan_bundle(input.packaged_artifacts.artifacts.iter().cloned());
    let domains = CanonicalSet::new(input.inspections.iter().map(|item| item.domain));
    let valid = input.inspections.len() == domains.len()
        && domains == required_secret_inspection_domains()
        && input.inspections.iter().all(|item| {
            item.inspected_bytes > 0
                && item.inspected_bytes <= secret_evidence_domain_byte_limit(item.domain)
                && item.leaks.is_empty()
        })
        && !input.sanitized_outputs.is_empty()
        && input
            .sanitized_outputs
            .iter()
            .all(|item| item.byte_count > 0 && item.byte_count <= MAX_INSPECTED_BYTES)
        && rebuilt_packaged_artifacts
            .as_ref()
            .is_ok_and(|rebuilt| rebuilt == &input.packaged_artifacts)
        && input.artifact_credentials_absent
        && input.verdict_credentials_absent
        && input.redaction_proven;
    if !valid {
        let code = if input.inspections.iter().any(|item| !item.leaks.is_empty()) {
            ConformanceDiagnosticCode::SecretCanaryLeak
        } else {
            ConformanceDiagnosticCode::EvidenceRedactionFailed
        };
        return Err(one_security_diagnostic(
            code,
            "secret inspection or durable evidence redaction is incomplete",
            None,
        ));
    }
    let sanitized_outputs = CanonicalSet::new(
        input
            .sanitized_outputs
            .iter()
            .map(|item| item.digest.clone()),
    );
    let report_digest = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &input.canary_set_digest,
            &domains,
            &sanitized_outputs,
            &input.packaged_artifacts,
        ),
    )
    .expect("validated secret evidence is canonically encodable");
    Ok(SecretEvidenceReport {
        canary_set_digest: input.canary_set_digest,
        inspected_domains: domains,
        sanitized_outputs,
        packaged_artifacts: input.packaged_artifacts,
        report_digest,
    })
}

/// Revalidates a persisted secret report from its retained safe identities.
pub fn validate_secret_evidence_report(
    report: &SecretEvidenceReport,
) -> Result<(), ConformanceDiagnosticSet> {
    let expected = canonical_digest(
        DigestDomain::ConformanceSecurity,
        &(
            &report.canary_set_digest,
            &report.inspected_domains,
            &report.sanitized_outputs,
            &report.packaged_artifacts,
        ),
    );
    if report.inspected_domains == required_secret_inspection_domains()
        && !report.sanitized_outputs.is_empty()
        && assemble_packaged_artifact_scan_bundle(
            report.packaged_artifacts.artifacts.iter().cloned(),
        )
        .as_ref()
        .is_ok_and(|rebuilt| rebuilt == &report.packaged_artifacts)
        && expected.is_ok_and(|digest| digest == report.report_digest)
    {
        return Ok(());
    }
    Err(one_security_diagnostic(
        ConformanceDiagnosticCode::EvidenceRedactionFailed,
        "persisted secret evidence is incomplete or identity-inconsistent",
        None,
    ))
}

/// Returns every output domain required by the secret gate.
pub fn required_secret_inspection_domains() -> CanonicalSet<SecretInspectionDomain> {
    use SecretInspectionDomain as Domain;
    CanonicalSet::new([
        Domain::SourceFiles,
        Domain::GeneratedAndPackagedFiles,
        Domain::ArtifactEntries,
        Domain::CacheAndProvenance,
        Domain::ProcessOutput,
        Domain::ErrorsAndDebug,
        Domain::DiagnosticsAndTraces,
        Domain::Reports,
        Domain::DraftVerdict,
    ])
}

fn cargo_root(manifest: &str, lockfile: &str) -> SupportedCargoRoot {
    SupportedCargoRoot {
        manifest: relative(manifest),
        lockfile: relative(lockfile),
    }
}

fn provenance_record(
    id: &str,
    role: ExternalInputRole,
    publisher: &str,
    repository: &str,
    immutable_digest: &str,
    review_evidence_digest: &str,
) -> ProvenanceRecord {
    ProvenanceRecord {
        id: ProvenanceId::new(id).expect("checked provenance ID is valid"),
        role,
        publisher: NonEmptyText::new(publisher).expect("checked publisher is valid"),
        repository: RepositoryId::new(repository).expect("checked repository is valid"),
        immutable_digest: Digest::new(immutable_digest).expect("checked digest is valid"),
        review_evidence_digest: Digest::new(review_evidence_digest)
            .expect("checked review digest is valid"),
    }
}

fn provenance_record_with_review_evidence(
    id: &str,
    role: ExternalInputRole,
    publisher: &str,
    repository: &str,
    immutable_digest: &str,
    review_evidence: &[u8],
) -> ProvenanceRecord {
    ProvenanceRecord {
        id: ProvenanceId::new(id).expect("checked provenance ID is valid"),
        role,
        publisher: NonEmptyText::new(publisher).expect("checked publisher is valid"),
        repository: RepositoryId::new(repository).expect("checked repository is valid"),
        immutable_digest: Digest::new(immutable_digest).expect("checked digest is valid"),
        review_evidence_digest: Digest::sha256(review_evidence),
    }
}

fn relative(path: &str) -> RepositoryRelativePath {
    RepositoryRelativePath::new(path).expect("checked repository-relative path is valid")
}

fn provenance_role_matches(record: &ProvenanceRecord) -> bool {
    let expected = match record.role {
        ExternalInputRole::ArtifactBuilderImage => {
            ("docker-official-images", "github.com/docker-library/rust")
        }
        ExternalInputRole::EngineBaseImage | ExternalInputRole::PreflightEngine => {
            ("dagger", "github.com/dagger/dagger")
        }
        ExternalInputRole::RustToolchain => ("rust-project", "github.com/rust-lang/rust"),
        ExternalInputRole::GoToolchain => {
            ("docker-official-images", "github.com/docker-library/golang")
        }
        ExternalInputRole::PreflightCli => {
            ("dagger-rust-sdk-maintainers", "github.com/dagger/dagger")
        }
        ExternalInputRole::CliArchive => ("dagger", "github.com/dagger/dagger"),
        ExternalInputRole::ScannerImage => ("aqua-security", "github.com/aquasecurity/trivy"),
        ExternalInputRole::VulnerabilityDatabaseSource => {
            ("aqua-security", "github.com/aquasecurity/trivy-db")
        }
    };
    record.publisher.as_str() == expected.0
        && record.repository.as_str() == expected.1
        && record.immutable_digest != Digest::sha256([])
        && record.review_evidence_digest != Digest::sha256([])
}

fn expiry_is_true(predicate: &ExpiryPredicate, context: &ExceptionEvaluationContext) -> bool {
    match predicate {
        ExpiryPredicate::FixedDate { expires_on } => context.current_date >= *expires_on,
        ExpiryPredicate::TargetRevision { reviewed_revision } => {
            context.target_revision != *reviewed_revision
        }
        ExpiryPredicate::PatchedVersion {
            package,
            patched_version,
        } => context
            .fixed_versions
            .get(package)
            .is_some_and(|version| version >= patched_version),
        ExpiryPredicate::AdvisoryWithdrawal { advisory } => {
            context.withdrawn_advisories.contains(advisory)
        }
    }
}

fn contains_bytes(haystack: &[u8], needle: &[u8]) -> bool {
    !needle.is_empty()
        && haystack
            .windows(needle.len())
            .any(|window| window == needle)
}

fn contains_ascii_case_insensitive(haystack: &[u8], needle: &[u8]) -> bool {
    haystack.windows(needle.len()).any(|window| {
        window
            .iter()
            .zip(needle)
            .all(|(left, right)| left.eq_ignore_ascii_case(right))
    })
}

fn contains_absolute_host_path(bytes: &[u8]) -> bool {
    bytes.starts_with(b"/")
        || bytes.windows(2).any(|window| window == b"\"/")
        || bytes
            .windows(2)
            .any(|window| window[0].is_ascii_whitespace() && window[1] == b'/')
        || bytes.windows(3).any(|window| {
            window[0].is_ascii_alphabetic() && window[1] == b':' && window[2] == b'\\'
        })
}

fn lower_hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut encoded = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        encoded.push(char::from(HEX[usize::from(byte >> 4)]));
        encoded.push(char::from(HEX[usize::from(byte & 0x0f)]));
    }
    encoded
}

fn one_security_diagnostic(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
    finding_id: Option<FindingId>,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([security_diagnostic(code, detail, finding_id)])
        .expect("security diagnostic is non-empty")
}

fn security_diagnostic(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
    finding_id: Option<FindingId>,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Security),
            finding_id,
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dates_validate_leap_years_and_canonical_shape() {
        assert!(UtcDate::new("2028-02-29").is_ok());
        assert!(UtcDate::new("2027-02-29").is_err());
        assert!(UtcDate::new("2027-2-01").is_err());
        assert!(UtcDate::new("２０２７-01-01").is_err());
    }

    #[test]
    fn canary_values_have_only_a_durable_digest_boundary() {
        let entropy = (0_u8..32).collect::<Vec<_>>();
        let canaries = secret_canary_set_from_entropy(&entropy).unwrap();
        assert_eq!(canaries.values.len(), 6);
        assert!(serde_json::to_value(canaries.digest()).is_ok());
    }

    #[test]
    fn live_canary_generation_uses_fresh_operating_system_entropy() {
        let first = generate_secret_canary_set().unwrap();
        let second = generate_secret_canary_set().unwrap();
        assert_ne!(first.digest(), second.digest());
    }
}
