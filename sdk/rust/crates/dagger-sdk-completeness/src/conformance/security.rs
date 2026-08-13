//! Locked Rust dependency, external provenance, vulnerability, and retained-evidence policy.
//!
//! Live canary and host-identity bytes remain in non-serializable values. Durable artifacts carry
//! only immutable identities, complete finding records, safe coordinates, and bounded outcomes.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

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
    ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, FindingId, ProvenanceId,
};

const MAX_INSPECTED_BYTES: u64 = 16 * 1024 * 1024;

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
    let preflight_review =
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
                "binary/preflight/a8789093",
                Role::PreflightCli,
                "dagger-rust-sdk-maintainers",
                "github.com/dagger/dagger",
                "sha256:a8789093fdfe61d47e93ac62c5556bfd6fcfba9409850cc020d822558f193a1e",
                preflight_review,
            ),
            provenance_record(
                "image/preflight-engine/beta.9",
                Role::PreflightEngine,
                "dagger",
                "github.com/dagger/dagger",
                "sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e",
                preflight_review,
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
            provenance_record(
                "source/trivy-db/0e0340a01b57209346d88dd061342a857776e403",
                Role::VulnerabilityDatabaseSource,
                "aqua-security",
                "github.com/aquasecurity/trivy-db",
                "sha256:c2f4a18d6a217f0f65c6d71b0b9a1f80ec4070af700e152df810d15ac46c4c68",
                "sha256:463084208d2fcf4cb6c0dab75f07dfaacdbcf19948022fe381102e2d11b9803b",
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
        .contains_key(&ExternalInputRole::VulnerabilityDatabaseSource);
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
        if inspected_bytes > MAX_INSPECTED_BYTES {
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
    let domains = CanonicalSet::new(input.inspections.iter().map(|item| item.domain));
    let valid = input.inspections.len() == domains.len()
        && domains == required_secret_inspection_domains()
        && input.inspections.iter().all(|item| {
            item.inspected_bytes > 0
                && item.inspected_bytes <= MAX_INSPECTED_BYTES
                && item.leaks.is_empty()
        })
        && !input.sanitized_outputs.is_empty()
        && input
            .sanitized_outputs
            .iter()
            .all(|item| item.byte_count > 0 && item.byte_count <= MAX_INSPECTED_BYTES)
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
        &(&input.canary_set_digest, &domains, &sanitized_outputs),
    )
    .expect("validated secret evidence is canonically encodable");
    Ok(SecretEvidenceReport {
        canary_set_digest: input.canary_set_digest,
        inspected_domains: domains,
        sanitized_outputs,
        report_digest,
    })
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
