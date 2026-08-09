//! Exact-target conformance category admission and evidence projection.
//!
//! Live coverage is representative by generated runtime strategy, while compile and
//! projection suites remain exhaustive by coordinate. A run is admissible only when it
//! covers the complete finite category matrix and scopes each observation to bindings
//! that actually require exact-target evidence.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    CanonicalSet, CapabilityId, CheckOutcome, CommandSpec, CommitSha, Digest, EvidenceId,
};

use super::evidence::{CoreCodegenEvidenceRecord, CoreCodegenEvidenceResult};
use super::manifest::{EvidenceDomain, GeneratedBindingManifest};

/// One generated runtime behaviour category required from the exact target.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ConformanceCategory {
    /// Built-in scalar execution and decoding.
    Scalar,
    /// Opaque custom-scalar round-trip.
    CustomScalar,
    /// Enum argument or result round-trip.
    Enum,
    /// Input-object serialization including omission.
    InputObject,
    /// Non-null object selection remains lazy until execution.
    LazyObject,
    /// Interface result or conversion through generated bindings.
    Interface,
    /// Nullable object presence and absence.
    NullableHandle,
    /// Ordered object-list re-entry.
    ObjectList,
    /// Expected-type argument supplied from a raw ID.
    ExpectedTypeRawId,
    /// Expected-type argument supplied from a compatible handle.
    ExpectedTypeHandle,
    /// ID-returning self operation re-enters the declared parent type.
    SelfReentry,
    /// GraphQL Void maps to Rust unit.
    Void,
    /// Explicit false, zero, empty string, or empty list is not omission.
    ExplicitZeroLike,
    /// Shared-session close fencing is preserved.
    Close,
    /// Shared-session request timeout is preserved.
    Timeout,
    /// Transport failure remains the typed runtime error.
    TransportError,
    /// GraphQL error payload remains available.
    GraphqlError,
    /// Engine-domain execution error remains typed.
    EngineError,
    /// Invalid response shape remains a typed decode error.
    DecodeError,
}

/// One successful live observation and its exact binding scope.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ConformanceObservation {
    /// Generated runtime category exercised by the operation.
    pub category: ConformanceCategory,
    /// Stable operation identity without raw output or credentials.
    pub operation: String,
    /// Recorded result.
    pub outcome: CheckOutcome,
    /// Only capabilities whose runtime strategy was observed by this case.
    pub capability_ids: CanonicalSet<CapabilityId>,
}

/// Complete deterministic result of one exact-target conformance command.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CoreConformanceRun {
    /// Exact target revision started by the repository workflow.
    pub target_revision: CommitSha,
    /// Exact schema digest used to generate the client.
    pub schema_digest: Digest,
    /// Content identity of the source under test.
    pub subject_revision: Digest,
    /// Portable command used by the workflow.
    pub command: CommandSpec,
    /// One observation for every required category.
    pub observations: Vec<ConformanceObservation>,
    /// Domain-separated digest of `observations`.
    pub result_digest: Digest,
}

/// Returns the finite category matrix a live run must exhaust.
#[must_use]
pub fn required_conformance_categories() -> BTreeSet<ConformanceCategory> {
    use ConformanceCategory::{
        Close, CustomScalar, DecodeError, EngineError, Enum, ExpectedTypeHandle, ExpectedTypeRawId,
        ExplicitZeroLike, GraphqlError, InputObject, Interface, LazyObject, NullableHandle,
        ObjectList, Scalar, SelfReentry, Timeout, TransportError, Void,
    };
    [
        Scalar,
        CustomScalar,
        Enum,
        InputObject,
        LazyObject,
        Interface,
        NullableHandle,
        ObjectList,
        ExpectedTypeRawId,
        ExpectedTypeHandle,
        SelfReentry,
        Void,
        ExplicitZeroLike,
        Close,
        Timeout,
        TransportError,
        GraphqlError,
        EngineError,
        DecodeError,
    ]
    .into()
}

/// Validates a complete live run and projects only its observed scope into evidence.
pub fn core_conformance_evidence(
    evidence_id: EvidenceId,
    run: &CoreConformanceRun,
    manifest: &GeneratedBindingManifest,
    expected_command_digest: &Digest,
) -> Validation<CoreCodegenEvidenceRecord> {
    let mut diagnostics = DiagnosticCollector::default();
    let subject = evidence_id.to_string();
    if run.target_revision.as_str() != manifest.target_revision
        || run.schema_digest.as_str() != manifest.schema_digest
    {
        diagnostics.push(conformance_diagnostic(
            DiagnosticCode::EvidenceTargetMismatch,
            &subject,
            "conformance target differs from the generated binding manifest",
        ));
    }
    let command_digest = canonical_digest(DigestDomain::Artifact, &run.command).ok();
    if command_digest.as_ref() != Some(expected_command_digest) {
        diagnostics.push(conformance_diagnostic(
            DiagnosticCode::EvidenceCommandInvalid,
            &subject,
            "conformance command differs from the reviewed exact-target command",
        ));
    }
    if canonical_digest(DigestDomain::Artifact, &run.observations)
        .ok()
        .as_ref()
        != Some(&run.result_digest)
    {
        diagnostics.push(conformance_diagnostic(
            DiagnosticCode::EvidenceOutcomeMissing,
            &subject,
            "conformance result digest differs from its structured observations",
        ));
    }

    let mut observed_categories = BTreeSet::new();
    let mut capability_ids = BTreeSet::new();
    for observation in &run.observations {
        if !observed_categories.insert(observation.category) {
            diagnostics.push(conformance_diagnostic(
                DiagnosticCode::ConformanceCoverageIncomplete,
                &subject,
                format!(
                    "category {:?} was observed more than once",
                    observation.category
                ),
            ));
        }
        if observation.outcome != CheckOutcome::Passed
            || observation.operation.trim().is_empty()
            || observation.operation.chars().any(char::is_control)
            || observation.capability_ids.is_empty()
        {
            diagnostics.push(conformance_diagnostic(
                DiagnosticCode::EvidenceObservationInvalid,
                &subject,
                format!(
                    "category {:?} has no passing scoped observation",
                    observation.category
                ),
            ));
        }
        for capability_id in observation.capability_ids.iter() {
            match manifest.bindings.get(capability_id) {
                Some(binding)
                    if binding
                        .required_evidence
                        .contains(&EvidenceDomain::ExactTarget) =>
                {
                    capability_ids.insert(capability_id.clone());
                }
                Some(_) => diagnostics.push(conformance_diagnostic(
                    DiagnosticCode::EvidenceObservationInvalid,
                    &subject,
                    format!("{capability_id} does not declare exact-target evidence"),
                )),
                None => diagnostics.push(conformance_diagnostic(
                    DiagnosticCode::EvidenceObservationInvalid,
                    &subject,
                    format!("{capability_id} is absent from the binding manifest"),
                )),
            }
        }
    }
    if observed_categories != required_conformance_categories() {
        diagnostics.push(conformance_diagnostic(
            DiagnosticCode::ConformanceCoverageIncomplete,
            &subject,
            "conformance observations do not exhaust the required category matrix",
        ));
    }

    let capability_ids = CanonicalSet::new(capability_ids);
    let scoped = capability_ids.iter().cloned().collect::<Vec<_>>();
    let capability_scope_digest = serde_json::to_vec(&scoped)
        .map(Digest::sha256)
        .unwrap_or_else(|_| Digest::sha256([]));
    let result = CoreCodegenEvidenceResult {
        outcome: CheckOutcome::Passed,
        assertion: "exact-target generated-client category matrix passed".to_owned(),
        capability_scope_digest,
    };
    let result_digest =
        canonical_digest(DigestDomain::Artifact, &result).unwrap_or_else(|_| Digest::sha256([]));
    let implementation_fingerprints = capability_ids
        .iter()
        .filter_map(|capability_id| {
            manifest.bindings.get(capability_id).map(|binding| {
                (
                    capability_id.clone(),
                    binding.implementation_fingerprint.clone(),
                )
            })
        })
        .collect::<BTreeMap<_, _>>();

    diagnostics.finish(CoreCodegenEvidenceRecord {
        evidence_id,
        target_revision: run.target_revision.clone(),
        schema_digest: run.schema_digest.clone(),
        subject_revision: run.subject_revision.clone(),
        command: run.command.clone(),
        result,
        result_digest,
        capability_ids,
        projection_fingerprint: manifest.projection_fingerprint.clone(),
        implementation_fingerprints,
        domains: [EvidenceDomain::ExactTarget].into(),
    })
}

fn conformance_diagnostic(
    code: DiagnosticCode,
    subject: impl ToString,
    detail: impl Into<String>,
) -> ContractDiagnostic {
    ContractDiagnostic::new(code, subject.to_string(), None, detail)
}
