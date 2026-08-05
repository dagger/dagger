//! Stable diagnostics and fail-closed validation results.
//!
//! Contract validation reports every independent defect it can establish in one pass. Diagnostics
//! are therefore accumulated and sorted by their durable external representation before being
//! returned. Tool failures are kept separate from contract defects and redact machine-local or
//! secret-bearing operating-system messages by construction.

use std::cmp::Ordering;
use std::fmt;
use std::io;
use std::str::FromStr;

use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use thiserror::Error;

use crate::model::{ExecutableId, RepositoryRelativePath, SourceLocator};

macro_rules! diagnostic_codes {
    ($( $variant:ident => $code:literal, )+) => {
        #[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
        /// Stable, machine-readable identifier for a contract integrity defect.
        ///
        /// The serialized spelling is part of the durable artifact format; enum declaration order
        /// has no semantic meaning.
        pub enum DiagnosticCode {
            $( $variant, )+
        }

        impl DiagnosticCode {
            /// Every diagnostic code supported by this contract format.
            pub const ALL: &'static [Self] = &[
                $( Self::$variant, )+
            ];

            /// Returns the exact durable spelling of this code.
            pub const fn as_str(self) -> &'static str {
                match self {
                    $( Self::$variant => $code, )+
                }
            }
        }

        impl FromStr for DiagnosticCode {
            type Err = UnknownDiagnosticCode;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                match value {
                    $( $code => Ok(Self::$variant), )+
                    _ => Err(UnknownDiagnosticCode(value.to_owned())),
                }
            }
        }
    };
}

diagnostic_codes! {
    FormatUnsupported => "FORMAT_UNSUPPORTED",
    TargetRepositoryInvalid => "TARGET_REPOSITORY_INVALID",
    TargetRevisionInvalid => "TARGET_REVISION_INVALID",
    TargetVersionMismatch => "TARGET_VERSION_MISMATCH",
    SchemaVersionMismatch => "SCHEMA_VERSION_MISMATCH",
    SchemaDigestMismatch => "SCHEMA_DIGEST_MISMATCH",
    GoAuthorityInvalid => "GO_AUTHORITY_INVALID",
    GoRevisionMismatch => "GO_REVISION_MISMATCH",
    GoVersionLabelMismatch => "GO_VERSION_LABEL_MISMATCH",
    RustTargetMismatch => "RUST_TARGET_MISMATCH",
    SdkContractAuthorityInvalid => "SDK_CONTRACT_AUTHORITY_INVALID",
    SdkContractRevisionMismatch => "SDK_CONTRACT_REVISION_MISMATCH",
    SdkContractTargetMismatch => "SDK_CONTRACT_TARGET_MISMATCH",
    AuthorityDuplicate => "AUTHORITY_DUPLICATE",
    AuthorityClassInvalid => "AUTHORITY_CLASS_INVALID",
    AuthorityRepositoryInvalid => "AUTHORITY_REPOSITORY_INVALID",
    AuthorityRevisionMismatch => "AUTHORITY_REVISION_MISMATCH",
    AuthoritySourceEmpty => "AUTHORITY_SOURCE_EMPTY",
    AuthorityExclusionInvalid => "AUTHORITY_EXCLUSION_INVALID",
    AuthorityExtractorInvalid => "AUTHORITY_EXTRACTOR_INVALID",
    AuthorityDrift => "AUTHORITY_DRIFT",
    CapabilityDuplicate => "CAPABILITY_DUPLICATE",
    CapabilityAuthorityMissing => "CAPABILITY_AUTHORITY_MISSING",
    CapabilityKindInvalid => "CAPABILITY_KIND_INVALID",
    CapabilitySourceMissing => "CAPABILITY_SOURCE_MISSING",
    CapabilitySummaryMissing => "CAPABILITY_SUMMARY_MISSING",
    CapabilitySignatureInvalid => "CAPABILITY_SIGNATURE_INVALID",
    CapabilityFingerprintMismatch => "CAPABILITY_FINGERPRINT_MISMATCH",
    CapabilityStatusInvalid => "CAPABILITY_STATUS_INVALID",
    CapabilityStabilityInvalid => "CAPABILITY_STABILITY_INVALID",
    CapabilityGapInvalid => "CAPABILITY_GAP_INVALID",
    CapabilityOwnerMissing => "CAPABILITY_OWNER_MISSING",
    ClassificationRuleDuplicate => "CLASSIFICATION_RULE_DUPLICATE",
    ClassificationSelectorInvalid => "CLASSIFICATION_SELECTOR_INVALID",
    ClassificationOverrideInvalid => "CLASSIFICATION_OVERRIDE_INVALID",
    LedgerDrift => "LEDGER_DRIFT",
    ImplementationEvidenceMissing => "IMPLEMENTATION_EVIDENCE_MISSING",
    VerificationEvidenceMissing => "VERIFICATION_EVIDENCE_MISSING",
    DecisionEvidenceInvalid => "DECISION_EVIDENCE_INVALID",
    EvidenceKindInvalid => "EVIDENCE_KIND_INVALID",
    EvidenceRepositoryInvalid => "EVIDENCE_REPOSITORY_INVALID",
    EvidenceRevisionMismatch => "EVIDENCE_REVISION_MISMATCH",
    EvidencePathInvalid => "EVIDENCE_PATH_INVALID",
    EvidenceLocatorInvalid => "EVIDENCE_LOCATOR_INVALID",
    EvidenceClaimMissing => "EVIDENCE_CLAIM_MISSING",
    EvidenceCommandInvalid => "EVIDENCE_COMMAND_INVALID",
    EvidenceOutcomeMissing => "EVIDENCE_OUTCOME_MISSING",
    EvidenceTargetMismatch => "EVIDENCE_TARGET_MISMATCH",
    EvidencePlatformInvalid => "EVIDENCE_PLATFORM_INVALID",
    HarnessCheckDuplicate => "HARNESS_CHECK_DUPLICATE",
    HarnessCheckKindInvalid => "HARNESS_CHECK_KIND_INVALID",
    HarnessRevisionMismatch => "HARNESS_REVISION_MISMATCH",
    HarnessCheckMissing => "HARNESS_CHECK_MISSING",
    HarnessCapabilityMissing => "HARNESS_CAPABILITY_MISSING",
    HarnessTargetMismatch => "HARNESS_TARGET_MISMATCH",
    HarnessPlatformInvalid => "HARNESS_PLATFORM_INVALID",
    HarnessInvocationInvalid => "HARNESS_INVOCATION_INVALID",
    HarnessOutcomeMissing => "HARNESS_OUTCOME_MISSING",
    HarnessEvidenceInvalid => "HARNESS_EVIDENCE_INVALID",
    HarnessScopeInvalid => "HARNESS_SCOPE_INVALID",
    TransitionBaseInvalid => "TRANSITION_BASE_INVALID",
    TransitionDiffInvalid => "TRANSITION_DIFF_INVALID",
    TransitionSemverInvalid => "TRANSITION_SEMVER_INVALID",
    TransitionMigrationMissing => "TRANSITION_MIGRATION_MISSING",
    CompatibilityTargetInvalid => "COMPATIBILITY_TARGET_INVALID",
    CompatibilityRangeInvalid => "COMPATIBILITY_RANGE_INVALID",
    CompatibilityEvidenceMissing => "COMPATIBILITY_EVIDENCE_MISSING",
    CompatibilityResponseMissing => "COMPATIBILITY_RESPONSE_MISSING",
    CompatibilityDrift => "COMPATIBILITY_DRIFT",
    ReportTargetMismatch => "REPORT_TARGET_MISMATCH",
    ReportDigestMismatch => "REPORT_DIGEST_MISMATCH",
    ReportVerdictInvalid => "REPORT_VERDICT_INVALID",
    ReportCountMismatch => "REPORT_COUNT_MISMATCH",
    ReportErrorSetMismatch => "REPORT_ERROR_SET_MISMATCH",
    ReportBlockerSetMismatch => "REPORT_BLOCKER_SET_MISMATCH",
    ReportExceptionSetMismatch => "REPORT_EXCEPTION_SET_MISMATCH",
}

impl fmt::Display for DiagnosticCode {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

impl Ord for DiagnosticCode {
    fn cmp(&self, other: &Self) -> Ordering {
        // Reports must remain stable when variants are regrouped in source, so ordering follows
        // the external contract spelling rather than the enum declaration.
        self.as_str().cmp(other.as_str())
    }
}

impl PartialOrd for DiagnosticCode {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl Serialize for DiagnosticCode {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(self.as_str())
    }
}

impl<'de> Deserialize<'de> for DiagnosticCode {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        Self::from_str(&value).map_err(D::Error::custom)
    }
}

#[derive(Clone, Debug, Eq, Error, PartialEq)]
#[error("unknown diagnostic code {0}")]
/// An external artifact used a diagnostic code unknown to this contract format.
pub struct UnknownDiagnosticCode(String);

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// One durable, machine-readable contract defect with human review context.
pub struct ContractDiagnostic {
    pub code: DiagnosticCode,
    pub subject: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub locator: Option<SourceLocator>,
    pub detail: String,
}

impl ContractDiagnostic {
    /// Creates a diagnostic without weakening the scalar types used by durable locators.
    pub fn new(
        code: DiagnosticCode,
        subject: impl Into<String>,
        locator: Option<SourceLocator>,
        detail: impl Into<String>,
    ) -> Self {
        Self {
            code,
            subject: subject.into(),
            locator,
            detail: detail.into(),
        }
    }
}

impl Ord for ContractDiagnostic {
    fn cmp(&self, other: &Self) -> Ordering {
        (&self.code, &self.subject, &self.locator, &self.detail).cmp(&(
            &other.code,
            &other.subject,
            &other.locator,
            &other.detail,
        ))
    }
}

impl PartialOrd for ContractDiagnostic {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// A non-empty, deterministically ordered collection of contract diagnostics.
pub struct DiagnosticSet(Vec<ContractDiagnostic>);

impl DiagnosticSet {
    /// Normalizes diagnostics, returning `None` when the input contains no defects.
    pub fn new(diagnostics: impl IntoIterator<Item = ContractDiagnostic>) -> Option<Self> {
        let mut diagnostics = diagnostics.into_iter().collect::<Vec<_>>();
        if diagnostics.is_empty() {
            return None;
        }
        // Validators discover defects in traversal order, which is an implementation detail. Sort
        // at the boundary so reports and tests cannot accidentally depend on that traversal.
        diagnostics.sort_unstable();
        Some(Self(diagnostics))
    }

    /// Borrows diagnostics in their stable report order.
    pub fn as_slice(&self) -> &[ContractDiagnostic] {
        &self.0
    }

    /// Returns the normalized diagnostics in their stable report order.
    pub fn into_inner(self) -> Vec<ContractDiagnostic> {
        self.0
    }
}

impl fmt::Display for DiagnosticSet {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{} contract diagnostic(s)", self.0.len())
    }
}

impl std::error::Error for DiagnosticSet {}

#[derive(Debug, Default)]
/// Accumulates independent validation defects before producing one fail-closed result.
pub struct DiagnosticCollector {
    diagnostics: Vec<ContractDiagnostic>,
}

impl DiagnosticCollector {
    /// Records one independently established defect.
    pub fn push(&mut self, diagnostic: ContractDiagnostic) {
        self.diagnostics.push(diagnostic);
    }

    /// Records multiple independently established defects.
    pub fn extend(&mut self, diagnostics: impl IntoIterator<Item = ContractDiagnostic>) {
        self.diagnostics.extend(diagnostics);
    }

    /// Returns `value` only when no defect was recorded.
    ///
    /// The candidate value is intentionally withheld on failure so callers cannot continue with a
    /// partially validated contract.
    pub fn finish<T>(self, value: T) -> Validation<T> {
        match DiagnosticSet::new(self.diagnostics) {
            Some(diagnostics) => Err(diagnostics),
            None => Ok(value),
        }
    }
}

/// Result of contract validation: a complete value or a stable non-empty defect set.
pub type Validation<T> = Result<T, DiagnosticSet>;

#[derive(Debug, Error)]
/// Operational failure of the completeness tool rather than a defect in contract data.
///
/// Variants retain only bounded, non-sensitive context suitable for terminal and CI output.
pub enum ToolError {
    #[error("{operation} failed with I/O kind {kind:?}")]
    Io {
        operation: &'static str,
        kind: io::ErrorKind,
        path: Option<RepositoryRelativePath>,
    },
    #[error("process {program} failed with exit status {status:?}")]
    Process {
        program: ExecutableId,
        status: Option<i32>,
    },
    #[error("could not decode {artifact}")]
    Decode { artifact: &'static str },
}

impl ToolError {
    /// Process exit status reserved for operational tool failure.
    pub const EXIT_STATUS: u8 = 2;

    /// Converts an I/O failure into redacted diagnostic context.
    ///
    /// Raw [`io::Error`] messages are deliberately discarded because they can contain absolute
    /// host paths, credentials, or command output.
    pub fn io(
        operation: &'static str,
        error: &io::Error,
        path: Option<RepositoryRelativePath>,
    ) -> Self {
        Self::Io {
            operation,
            kind: error.kind(),
            path,
        }
    }
}

#[cfg(test)]
mod tests {
    use pretty_assertions::assert_eq;

    use super::*;

    #[test]
    fn every_diagnostic_code_round_trips_through_its_exact_spelling() {
        for code in DiagnosticCode::ALL {
            let encoded = serde_json::to_string(code).unwrap();
            assert_eq!(encoded, format!("\"{}\"", code.as_str()));
            assert_eq!(
                serde_json::from_str::<DiagnosticCode>(&encoded).unwrap(),
                *code
            );
        }
    }

    #[test]
    fn unknown_diagnostic_codes_are_rejected() {
        assert!(serde_json::from_str::<DiagnosticCode>("\"UNKNOWN\"").is_err());
    }

    #[test]
    fn diagnostic_sets_have_deterministic_lexical_order() {
        let set = DiagnosticSet::new([
            ContractDiagnostic::new(
                DiagnosticCode::TargetRevisionInvalid,
                "target",
                None,
                "revision",
            ),
            ContractDiagnostic::new(DiagnosticCode::GoRevisionMismatch, "go", None, "revision"),
        ])
        .unwrap();

        assert_eq!(set.as_slice()[0].code, DiagnosticCode::GoRevisionMismatch);
        assert_eq!(
            set.as_slice()[1].code,
            DiagnosticCode::TargetRevisionInvalid
        );
    }

    #[test]
    fn tool_errors_do_not_retain_underlying_error_messages() {
        let secret = io::Error::new(io::ErrorKind::PermissionDenied, "token=secret");
        let error = ToolError::io("read source", &secret, None);

        assert!(!error.to_string().contains("secret"));
        assert_eq!(ToolError::EXIT_STATUS, 2);
    }
}
