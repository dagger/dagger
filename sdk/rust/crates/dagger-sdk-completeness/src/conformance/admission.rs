//! Final evidence admission, ledger-transition derivation, and phase-separated reporting.
//!
//! A complete exact-engine verdict is intentionally downstream of applicability,
//! implementation, platform, and security closure. Keeping those phases separate here prevents
//! a passing intermediate gate from being rendered as SDK sign-off or release permission.

#![warn(missing_docs)]

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use crate::model::{CanonicalSet, ClassificationValues, EvidenceId, Status, TargetDigest};
use crate::traceability::CandidateStatusChanges;

use super::{
    ApplicabilityDisposition, ArtifactSecurityReport, AtomicSignoffVerdict, ConformanceDiagnostic,
    ConformanceDiagnosticCode, ConformanceDiagnosticSet, ConformanceFormatVersion,
    ConformanceScope, DiagnosticCoordinate, DiagnosticPhase, ImplementationClosureBundle,
    SignoffPhaseTimings, SupportedNativePlatformSet, VerdictDecision,
};

const IMPLEMENTATION_EVIDENCE_ID: &str = "implementation/conformance-signoff";
const VERIFICATION_EVIDENCE_ID: &str = "verification/conformance-signoff";
const DECISION_EVIDENCE_ID: &str = "decision/conformance-applicability";

/// Independent state of one conformance reporting phase.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ConformancePhaseState {
    /// No current artifact was supplied for this phase.
    Missing,
    /// A current artifact was supplied and its identity is consistent.
    Complete,
    /// An atomic exact-engine verdict passed.
    Passed,
    /// An atomic exact-engine verdict failed without admitting a subset.
    Failed,
}

/// Result of rerunning checked report generation without changing its admitted inputs.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ConformanceReproductionState {
    /// No clean-output reproduction was supplied.
    NotRun,
    /// A second generation produced no diff.
    Clean,
    /// Checked output changed and must not be presented as reproducible.
    Drifted,
}

/// Neutral snapshot of independently established conformance closure phases.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConformanceReport {
    /// Durable report format.
    pub format_version: ConformanceFormatVersion,
    /// Exact target shared by every supplied phase.
    pub target_digest: TargetDigest,
    /// Reviewed applicability inventory size.
    pub applicability_capabilities: u32,
    /// Engine-free implementation closure state.
    pub implementation: ConformancePhaseState,
    /// Supported Linux/macOS native-platform closure state.
    pub native_platform: ConformancePhaseState,
    /// Exact-artifact security closure state.
    pub security: ConformancePhaseState,
    /// Atomic exact-engine sign-off state.
    pub exact_engine: ConformancePhaseState,
    /// Complete neutral destination counts, present only for a passing verdict.
    pub destination_counts: BTreeMap<Status, u32>,
    /// Number of scoped rows which remain blocking.
    pub remaining_blockers: u32,
    /// Current implementation closure identity when supplied.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub closure_bundle_digest: Option<crate::model::Digest>,
    /// Current supported native-platform identity when supplied.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub platform_matrix_digest: Option<crate::model::Digest>,
    /// Current security identity when supplied.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub security_report_digest: Option<crate::model::Digest>,
    /// Atomic exact-engine verdict identity when supplied.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub verdict_digest: Option<crate::model::Digest>,
    /// Canonical exact-target artifact manifest identity when sign-off ran.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub artifact_manifest_digest: Option<crate::model::Digest>,
    /// Direct identity of the exact retained OCI payload when sign-off ran.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub artifact_payload_digest: Option<crate::model::Digest>,
    /// Complete shared sign-off phase timings when sign-off ran.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub phase_timings: Option<SignoffPhaseTimings>,
    /// Independent checked-output reproduction result.
    pub reproducibility: ConformanceReproductionState,
}

/// Derives only the complete status proposal authorized by one passing verdict.
///
/// This function creates no ledger side effects. Its output still has to pass the evidence and
/// transition validators, which prevents a caller from bypassing canonical routing with a direct
/// ledger edit.
pub fn derive_conformance_status_changes(
    scope: &ConformanceScope,
    verdict: &AtomicSignoffVerdict,
) -> Result<CandidateStatusChanges, ConformanceDiagnosticSet> {
    let VerdictDecision::Passed { capability_ids } = &verdict.decision else {
        return Err(admission_error(
            "a failed exact-engine verdict cannot admit status changes",
        ));
    };
    if verdict.target_digest != *scope.target_digest() {
        return Err(admission_error(
            "the exact-engine verdict targets a different conformance scope",
        ));
    }

    let expected_engine_capabilities = CanonicalSet::new(
        scope
            .existing_records()
            .values()
            .filter(|record| {
                matches!(
                    record.disposition,
                    ApplicabilityDisposition::RustObservableSameMechanism
                        | ApplicabilityDisposition::RustObservableIdiomatic
                )
            })
            .map(|record| record.capability_id.clone()),
    );
    if verdict.claimed_capability_ids != *capability_ids
        || expected_engine_capabilities
            .iter()
            .any(|capability_id| !capability_ids.contains(capability_id))
    {
        return Err(admission_error(
            "the passing verdict does not prove the complete applicable capability set",
        ));
    }

    let implementation = evidence_id(IMPLEMENTATION_EVIDENCE_ID);
    let verification = evidence_id(VERIFICATION_EVIDENCE_ID);
    let decision = evidence_id(DECISION_EVIDENCE_ID);
    let mut changes = BTreeMap::new();
    for record in scope.existing_records().values() {
        let (status, implementation_evidence, verification_evidence, decision_evidence) =
            match record.disposition {
                ApplicabilityDisposition::RustObservableSameMechanism => (
                    Status::Implemented,
                    CanonicalSet::new([implementation.clone()]),
                    CanonicalSet::new([verification.clone()]),
                    CanonicalSet::default(),
                ),
                ApplicabilityDisposition::RustObservableIdiomatic => (
                    Status::IdiomaticEquivalent,
                    CanonicalSet::new([implementation.clone()]),
                    CanonicalSet::new([verification.clone()]),
                    CanonicalSet::new([decision.clone()]),
                ),
                ApplicabilityDisposition::EngineOwnedNoRustObligation
                | ApplicabilityDisposition::ForeignSdkNoRustObligation => (
                    Status::Inapplicable,
                    CanonicalSet::default(),
                    CanonicalSet::default(),
                    CanonicalSet::new([decision.clone()]),
                ),
            };
        changes.insert(
            record.capability_id.clone(),
            ClassificationValues {
                status,
                gap: None,
                owner_feature: None,
                implementation_evidence,
                verification_evidence,
                decision_evidence,
            },
        );
    }
    for capability_id in scope.policy_capabilities().keys() {
        changes.insert(
            capability_id.clone(),
            ClassificationValues {
                status: Status::Implemented,
                gap: None,
                owner_feature: None,
                implementation_evidence: CanonicalSet::new([implementation.clone()]),
                verification_evidence: CanonicalSet::new([verification.clone()]),
                decision_evidence: CanonicalSet::default(),
            },
        );
    }
    Ok(CandidateStatusChanges { changes })
}

/// Derives a neutral phase-separated report without promoting intermediate closure to sign-off.
pub fn derive_conformance_report(
    scope: &ConformanceScope,
    closure: Option<&ImplementationClosureBundle>,
    platform: Option<&SupportedNativePlatformSet>,
    security: Option<&ArtifactSecurityReport>,
    verdict: Option<&AtomicSignoffVerdict>,
    reproduction_clean: Option<bool>,
) -> Result<ConformanceReport, ConformanceDiagnosticSet> {
    if closure.is_some_and(|value| value.target_digest != *scope.target_digest())
        || platform.is_some_and(|value| value.target_digest != *scope.target_digest())
        || verdict.is_some_and(|value| value.target_digest != *scope.target_digest())
    {
        return Err(admission_error(
            "one or more report phases target a different conformance scope",
        ));
    }
    if let (Some(closure), Some(platform)) = (closure, platform)
        && closure.platform_matrix_digest != platform.observation_set_digest
    {
        return Err(admission_error(
            "implementation and native-platform closure identities differ",
        ));
    }
    if let (Some(security), Some(verdict)) = (security, verdict)
        && (security.report_digest != verdict.security_report_digest
            || security.artifact_manifest_digest != verdict.artifact_manifest_digest
            || security.artifact_payload_digest != verdict.artifact_payload_digest)
    {
        return Err(admission_error(
            "security closure does not describe the exact verdict artifact",
        ));
    }
    if let (Some(closure), Some(verdict)) = (closure, verdict)
        && closure.bundle_digest != verdict.closure_bundle_digest
    {
        return Err(admission_error(
            "implementation closure does not match the exact-engine verdict",
        ));
    }
    if let (Some(platform), Some(verdict)) = (platform, verdict)
        && platform.observation_set_digest != verdict.platform_matrix_digest
    {
        return Err(admission_error(
            "native-platform closure does not match the exact-engine verdict",
        ));
    }

    let transitions = verdict
        .filter(|value| matches!(value.decision, VerdictDecision::Passed { .. }))
        .map(|value| derive_conformance_status_changes(scope, value))
        .transpose()?;
    let mut destination_counts = BTreeMap::new();
    if let Some(transitions) = &transitions {
        for values in transitions.changes.values() {
            *destination_counts.entry(values.status.clone()).or_insert(0) += 1;
        }
    }
    let scoped_count = scope.existing_records().len() + scope.policy_capabilities().len();
    let admitted_count = transitions.as_ref().map_or(0, |value| value.changes.len());

    Ok(ConformanceReport {
        format_version: ConformanceFormatVersion::V1,
        target_digest: scope.target_digest().clone(),
        applicability_capabilities: u32::try_from(scoped_count)
            .expect("conformance scope count is bounded"),
        implementation: closure.map_or(ConformancePhaseState::Missing, |_| {
            ConformancePhaseState::Complete
        }),
        native_platform: platform.map_or(ConformancePhaseState::Missing, |_| {
            ConformancePhaseState::Complete
        }),
        security: security.map_or(ConformancePhaseState::Missing, |_| {
            ConformancePhaseState::Complete
        }),
        exact_engine: verdict.map_or(ConformancePhaseState::Missing, |value| {
            match value.decision {
                VerdictDecision::Passed { .. } => ConformancePhaseState::Passed,
                VerdictDecision::Failed { .. } => ConformancePhaseState::Failed,
            }
        }),
        destination_counts,
        remaining_blockers: u32::try_from(scoped_count - admitted_count)
            .expect("conformance scope count is bounded"),
        closure_bundle_digest: closure.map(|value| value.bundle_digest.clone()),
        platform_matrix_digest: platform.map(|value| value.observation_set_digest.clone()),
        security_report_digest: security.map(|value| value.report_digest.clone()),
        verdict_digest: verdict.map(|value| value.verdict_digest.clone()),
        artifact_manifest_digest: verdict.map(|value| value.artifact_manifest_digest.clone()),
        artifact_payload_digest: verdict.map(|value| value.artifact_payload_digest.clone()),
        phase_timings: verdict.map(|value| value.phase_timings.clone()),
        reproducibility: match reproduction_clean {
            None => ConformanceReproductionState::NotRun,
            Some(true) => ConformanceReproductionState::Clean,
            Some(false) => ConformanceReproductionState::Drifted,
        },
    })
}

/// Renders the five independent closure phases and neutral destination counts.
pub fn render_conformance_report(report: &ConformanceReport) -> String {
    let mut rendered = format!(
        "# Rust SDK conformance report\n\nTarget: `{}`\n\n| Phase | State |\n| --- | --- |\n| Applicability | complete |\n| Implementation | {} |\n| Native platform | {} |\n| Security | {} |\n| Exact engine | {} |\n| Reproducibility | {} |\n\nRemaining blockers: {}\n",
        report.target_digest,
        phase_label(report.implementation),
        phase_label(report.native_platform),
        phase_label(report.security),
        phase_label(report.exact_engine),
        reproduction_label(report.reproducibility),
        report.remaining_blockers,
    );
    if let (Some(manifest), Some(payload)) = (
        &report.artifact_manifest_digest,
        &report.artifact_payload_digest,
    ) {
        rendered.push_str(&format!(
            "\nArtifact manifest: `{manifest}`\n\nArtifact payload: `{payload}`\n"
        ));
    }
    if let Some(timings) = &report.phase_timings {
        rendered.push_str(&format!(
            "\nTotal sign-off time: {} ms\n",
            timings.total.get()
        ));
    }
    if !report.destination_counts.is_empty() {
        rendered
            .push_str("\n## Derived destination counts\n\n| Status | Count |\n| --- | ---: |\n");
        for (status, count) in &report.destination_counts {
            rendered.push_str(&format!("| `{status:?}` | {count} |\n"));
        }
    }
    rendered
}

const fn reproduction_label(state: ConformanceReproductionState) -> &'static str {
    match state {
        ConformanceReproductionState::NotRun => "not-run",
        ConformanceReproductionState::Clean => "clean",
        ConformanceReproductionState::Drifted => "drifted",
    }
}

fn evidence_id(value: &'static str) -> EvidenceId {
    EvidenceId::new(value).expect("reviewed conformance evidence identity is valid")
}

const fn phase_label(state: ConformancePhaseState) -> &'static str {
    match state {
        ConformancePhaseState::Missing => "missing",
        ConformancePhaseState::Complete => "complete",
        ConformancePhaseState::Passed => "passed",
        ConformancePhaseState::Failed => "failed",
    }
}

fn admission_error(detail: &'static str) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::SignoffVerdictIncomplete,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Verdict),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )])
    .expect("one diagnostic is present")
}
