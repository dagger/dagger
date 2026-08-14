//! Stable, bounded diagnostics for conformance compilation and SDK sign-off.
//!
//! Diagnostics retain semantic coordinates but never raw subprocess output, credentials,
//! absolute host paths, or provider identity. Normalization sorts and de-duplicates independent
//! defects so traversal and cleanup order cannot perturb durable evidence.

use std::cmp::Ordering;
use std::fmt;
use std::str::FromStr;

use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use thiserror::Error;

use crate::model::CapabilityId;

use super::{AssertionId, FindingId, SignoffCaseId};

const MAX_SAFE_DETAIL_BYTES: usize = 256;

macro_rules! diagnostic_codes {
    ($( $variant:ident => $code:literal, )+) => {
        #[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
        /// Stable machine-readable conformance or sign-off failure class.
        pub enum ConformanceDiagnosticCode {
            $( $variant, )+
        }

        impl ConformanceDiagnosticCode {
            /// Complete diagnostic vocabulary supported by this format.
            pub const ALL: &'static [Self] = &[$(Self::$variant,)+];

            /// Returns the exact durable code spelling.
            pub const fn as_str(self) -> &'static str {
                match self {
                    $(Self::$variant => $code,)+
                }
            }
        }

        impl FromStr for ConformanceDiagnosticCode {
            type Err = UnknownConformanceDiagnosticCode;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                match value {
                    $($code => Ok(Self::$variant),)+
                    _ => Err(UnknownConformanceDiagnosticCode),
                }
            }
        }
    };
}

diagnostic_codes! {
    ConformanceScopeChanged => "CONFORMANCE_SCOPE_CHANGED",
    ConformancePolicyScopeChanged => "CONFORMANCE_POLICY_SCOPE_CHANGED",
    ApplicabilityRecordInvalid => "APPLICABILITY_RECORD_INVALID",
    ApplicabilityDecisionInvalid => "APPLICABILITY_DECISION_INVALID",
    ConformanceAssertionInvalid => "CONFORMANCE_ASSERTION_INVALID",
    ConformanceCaseCatalogInvalid => "CONFORMANCE_CASE_CATALOG_INVALID",
    ConformanceCaseForbidden => "CONFORMANCE_CASE_FORBIDDEN",
    SignoffHostProfileInvalid => "SIGNOFF_HOST_PROFILE_INVALID",
    SignoffHostPreflightFailed => "SIGNOFF_HOST_PREFLIGHT_FAILED",
    SignoffHostSmokeInvalid => "SIGNOFF_HOST_SMOKE_INVALID",
    SignoffHostBoundaryInvalid => "SIGNOFF_HOST_BOUNDARY_INVALID",
    SignoffHostPreflightStale => "SIGNOFF_HOST_PREFLIGHT_STALE",
    ImplementationClosureIncomplete => "IMPLEMENTATION_CLOSURE_INCOMPLETE",
    ImplementationClosureBoundaryInvalid => "IMPLEMENTATION_CLOSURE_BOUNDARY_INVALID",
    SignoffArtifactStateInvalid => "SIGNOFF_ARTIFACT_STATE_INVALID",
    SignoffArtifactManifestInvalid => "SIGNOFF_ARTIFACT_MANIFEST_INVALID",
    SignoffArtifactPayloadInvalid => "SIGNOFF_ARTIFACT_PAYLOAD_INVALID",
    SignoffArtifactProvenanceInvalid => "SIGNOFF_ARTIFACT_PROVENANCE_INVALID",
    SignoffDuplicateWork => "SIGNOFF_DUPLICATE_WORK",
    SignoffArtifactImportFailed => "SIGNOFF_ARTIFACT_IMPORT_FAILED",
    SignoffEngineIdentityMismatch => "SIGNOFF_ENGINE_IDENTITY_MISMATCH",
    SignoffEngineLifecycleInvalid => "SIGNOFF_ENGINE_LIFECYCLE_INVALID",
    SignoffRustBaselineInvalid => "SIGNOFF_RUST_BASELINE_INVALID",
    SignoffDistributionObservationInvalid => "SIGNOFF_DISTRIBUTION_OBSERVATION_INVALID",
    SignoffCaseIsolationViolation => "SIGNOFF_CASE_ISOLATION_VIOLATION",
    SignoffCaseFailed => "SIGNOFF_CASE_FAILED",
    SignoffCaseSkipped => "SIGNOFF_CASE_SKIPPED",
    SignoffCaseUnknown => "SIGNOFF_CASE_UNKNOWN",
    SignoffRetryInvalid => "SIGNOFF_RETRY_INVALID",
    PlatformMatrixIncomplete => "PLATFORM_MATRIX_INCOMPLETE",
    PlatformClaimInvalid => "PLATFORM_CLAIM_INVALID",
    RustSecurityGateFailed => "RUST_SECURITY_GATE_FAILED",
    RustSecurityPolicyFailed => "RUST_SECURITY_POLICY_FAILED",
    ArtifactSecurityProvenanceInvalid => "ARTIFACT_SECURITY_PROVENANCE_INVALID",
    ArtifactVulnerabilityGateFailed => "ARTIFACT_VULNERABILITY_GATE_FAILED",
    ArtifactSecurityExceptionInvalid => "ARTIFACT_SECURITY_EXCEPTION_INVALID",
    SecretCanaryLeak => "SECRET_CANARY_LEAK",
    ConformanceCheckpointScopeInvalid => "CONFORMANCE_CHECKPOINT_SCOPE_INVALID",
    ConformanceCheckpointTimeout => "CONFORMANCE_CHECKPOINT_TIMEOUT",
    ConformanceCheckpointEvidenceInvalid => "CONFORMANCE_CHECKPOINT_EVIDENCE_INVALID",
    SignoffUnrelatedWork => "SIGNOFF_UNRELATED_WORK",
    SignoffVerdictIncomplete => "SIGNOFF_VERDICT_INCOMPLETE",
    SignoffReleaseHandoffInvalid => "SIGNOFF_RELEASE_HANDOFF_INVALID",
    EvidenceRedactionFailed => "EVIDENCE_REDACTION_FAILED",
}

impl fmt::Display for ConformanceDiagnosticCode {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

impl Ord for ConformanceDiagnosticCode {
    fn cmp(&self, other: &Self) -> Ordering {
        self.as_str().cmp(other.as_str())
    }
}

impl PartialOrd for ConformanceDiagnosticCode {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl Serialize for ConformanceDiagnosticCode {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(self.as_str())
    }
}

impl<'de> Deserialize<'de> for ConformanceDiagnosticCode {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        Self::from_str(&String::deserialize(deserializer)?).map_err(D::Error::custom)
    }
}

#[derive(Clone, Copy, Debug, Eq, Error, PartialEq)]
#[error("unknown conformance diagnostic code")]
/// An artifact named a diagnostic code outside the closed format vocabulary.
pub struct UnknownConformanceDiagnosticCode;

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Closed phase coordinate used by preflight and sign-off diagnostics.
pub enum DiagnosticPhase {
    Scope,
    Applicability,
    Catalog,
    HostProfile,
    HostResources,
    ContainerDaemon,
    PersistentCanary,
    ExportImport,
    CacheReuse,
    SmokeStart,
    SmokeProbe,
    SmokeStop,
    RetainedOutput,
    Cleanup,
    Closure,
    Artifact,
    Engine,
    Baseline,
    Case,
    Platform,
    Security,
    Checkpoint,
    Verdict,
}

#[derive(Clone, Debug, Default, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Optional stable coordinates locating a defect without retaining unsafe source text.
pub struct DiagnosticCoordinate {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub phase: Option<DiagnosticPhase>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub capability_id: Option<CapabilityId>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub assertion_id: Option<AssertionId>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub case_id: Option<SignoffCaseId>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub finding_id: Option<FindingId>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
/// Bounded diagnostic text proven free of controls, paths, and credential-shaped content.
pub struct SafeDiagnosticText(String);

impl SafeDiagnosticText {
    /// Validates authored stable text for durable evidence.
    pub fn new(value: impl Into<String>) -> Result<Self, &'static str> {
        let value = value.into();
        if value.is_empty() || value.len() > MAX_SAFE_DETAIL_BYTES {
            return Err("diagnostic text is empty or exceeds 256 bytes");
        }
        if value.trim() != value || value.chars().any(char::is_control) {
            return Err("diagnostic text contains whitespace controls");
        }
        if value.starts_with('/')
            || value.contains("file://")
            || value.contains("\\")
            || value.contains("token=")
            || value.contains("password=")
            || value.contains("authorization:")
        {
            return Err("diagnostic text contains a path or credential-shaped value");
        }
        Ok(Self(value))
    }

    /// Returns a stable replacement for untrusted adapter output.
    pub fn redacted() -> Self {
        Self("operational detail redacted".to_owned())
    }

    /// Borrows the validated text.
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl Serialize for SafeDiagnosticText {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(self.as_str())
    }
}

impl<'de> Deserialize<'de> for SafeDiagnosticText {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        Self::new(String::deserialize(deserializer)?).map_err(D::Error::custom)
    }
}

#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// One safe durable conformance defect.
pub struct ConformanceDiagnostic {
    pub code: ConformanceDiagnosticCode,
    pub coordinate: DiagnosticCoordinate,
    pub detail: SafeDiagnosticText,
}

impl ConformanceDiagnostic {
    /// Constructs a diagnostic from authored safe detail.
    pub fn new(
        code: ConformanceDiagnosticCode,
        coordinate: DiagnosticCoordinate,
        detail: &'static str,
    ) -> Self {
        Self {
            code,
            coordinate,
            detail: SafeDiagnosticText::new(detail)
                .expect("authored conformance diagnostic detail must be safe"),
        }
    }

    /// Constructs a diagnostic for an operational failure without retaining its raw cause.
    pub fn redacted(code: ConformanceDiagnosticCode, coordinate: DiagnosticCoordinate) -> Self {
        Self {
            code,
            coordinate,
            detail: SafeDiagnosticText::redacted(),
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Non-empty, sorted, de-duplicated conformance diagnostic collection.
pub struct ConformanceDiagnosticSet(Vec<ConformanceDiagnostic>);

impl ConformanceDiagnosticSet {
    /// Normalizes defects, returning `None` when there are none.
    pub fn new(diagnostics: impl IntoIterator<Item = ConformanceDiagnostic>) -> Option<Self> {
        let mut diagnostics = diagnostics.into_iter().collect::<Vec<_>>();
        diagnostics.sort_unstable();
        diagnostics.dedup();
        (!diagnostics.is_empty()).then_some(Self(diagnostics))
    }

    /// Borrows defects in stable report order.
    pub fn as_slice(&self) -> &[ConformanceDiagnostic] {
        &self.0
    }

    /// Returns normalized defects.
    pub fn into_inner(self) -> Vec<ConformanceDiagnostic> {
        self.0
    }
}

impl Serialize for ConformanceDiagnosticSet {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        self.0.serialize(serializer)
    }
}

impl<'de> Deserialize<'de> for ConformanceDiagnosticSet {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        Self::new(Vec::<ConformanceDiagnostic>::deserialize(deserializer)?)
            .ok_or_else(|| D::Error::custom("a diagnostic set must not be empty"))
    }
}

impl fmt::Display for ConformanceDiagnosticSet {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{} conformance diagnostic(s)", self.0.len())
    }
}

impl std::error::Error for ConformanceDiagnosticSet {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_diagnostic_code_round_trips() {
        for code in ConformanceDiagnosticCode::ALL {
            let json = serde_json::to_string(code).unwrap();
            assert_eq!(
                serde_json::from_str::<ConformanceDiagnosticCode>(&json).unwrap(),
                *code
            );
        }
    }

    #[test]
    fn hostile_diagnostic_text_is_rejected_and_redaction_is_safe() {
        for detail in [
            "/Users/person/secret",
            "token=real-secret",
            "line one\nline two",
            "authorization: bearer secret",
        ] {
            assert!(SafeDiagnosticText::new(detail).is_err());
        }
        assert_eq!(
            SafeDiagnosticText::redacted().as_str(),
            "operational detail redacted"
        );
    }

    #[test]
    fn diagnostic_sets_sort_and_deduplicate() {
        let item = ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostProfileInvalid,
            DiagnosticCoordinate::default(),
            "profile is invalid",
        );
        let set = ConformanceDiagnosticSet::new([item.clone(), item]).unwrap();
        assert_eq!(set.as_slice().len(), 1);
    }
}
