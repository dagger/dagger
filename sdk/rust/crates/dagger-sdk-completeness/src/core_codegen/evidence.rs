//! Capability-scoped executable evidence admission and freshness.
//!
//! Evidence is useful only while every identity that gave it meaning remains current.
//! Admission therefore binds target, subject, command, result, capability scope,
//! projection, implementation fingerprints, and evidence domains as one indivisible
//! record. Stale records remain visible for audit but satisfy no binding domain.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    CanonicalSet, CapabilityId, CheckOutcome, CommandSpec, CommitSha, Digest, EvidenceId,
};

use super::manifest::{EvidenceDomain, GeneratedBindingManifest};

/// Result facts whose digest is stored beside executable evidence.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CoreCodegenEvidenceResult {
    /// Recorded command outcome.
    pub outcome: CheckOutcome,
    /// Stable assertion identity rather than raw command output.
    pub assertion: String,
    /// Digest of the exact capability scope observed by the command.
    pub capability_scope_digest: Digest,
}

/// One immutable, capability-scoped executable evidence claim.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CoreCodegenEvidenceRecord {
    /// Identity duplicated from the registry key.
    pub evidence_id: EvidenceId,
    /// Exact Dagger revision exercised or compiled against.
    pub target_revision: CommitSha,
    /// Exact schema bytes represented by the generated client.
    pub schema_digest: Digest,
    /// Content identity of the reviewed source whose result is being registered.
    pub subject_revision: Digest,
    /// Portable argv-only command identity.
    pub command: CommandSpec,
    /// Structured result from which `result_digest` is recomputed.
    pub result: CoreCodegenEvidenceResult,
    /// Domain-separated digest of `result`.
    pub result_digest: Digest,
    /// Exact capabilities observed by this command.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Projection catalog identity used to generate the subject.
    pub projection_fingerprint: Digest,
    /// Per-capability implementation identities at execution time.
    pub implementation_fingerprints: BTreeMap<CapabilityId, Digest>,
    /// Evidence domains actually proved by this command.
    pub domains: BTreeSet<EvidenceDomain>,
}

/// Evidence records keyed by stable identity.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CoreCodegenEvidenceRegistry {
    /// Complete authored or emitted evidence set.
    pub records: BTreeMap<EvidenceId, CoreCodegenEvidenceRecord>,
}

/// Reviewed command and subject identities accepted for evidence admission.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CoreCodegenEvidencePolicy {
    /// Content identity of the source under verification.
    pub subject_revision: Digest,
    /// Exact command digest allowed to prove each evidence domain.
    pub command_digests: BTreeMap<EvidenceDomain, Digest>,
}

/// Conservative evidence closure; expired records never contribute domains.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct CoreCodegenEvidenceClosure {
    satisfied: BTreeMap<CapabilityId, BTreeSet<EvidenceDomain>>,
    closed: BTreeSet<CapabilityId>,
    expired: BTreeSet<EvidenceId>,
}

impl CoreCodegenEvidenceClosure {
    /// Returns the domains satisfied for one capability.
    #[must_use]
    pub fn satisfied_domains(
        &self,
        capability_id: &CapabilityId,
    ) -> Option<&BTreeSet<EvidenceDomain>> {
        self.satisfied.get(capability_id)
    }

    /// Returns capabilities whose complete declared domain set is satisfied.
    #[must_use]
    pub const fn closed_capability_ids(&self) -> &BTreeSet<CapabilityId> {
        &self.closed
    }

    /// Returns evidence records rejected as stale or malformed.
    #[must_use]
    pub const fn expired_evidence_ids(&self) -> &BTreeSet<EvidenceId> {
        &self.expired
    }
}

/// Joins only fresh evidence and reports closure without changing ledger status.
#[must_use]
pub fn verify_core_codegen_evidence(
    manifest: &GeneratedBindingManifest,
    registry: &CoreCodegenEvidenceRegistry,
    policy: &CoreCodegenEvidencePolicy,
) -> CoreCodegenEvidenceClosure {
    let mut closure = CoreCodegenEvidenceClosure::default();
    for (evidence_id, record) in &registry.records {
        if admit_core_codegen_evidence(evidence_id, record, manifest, policy).is_err() {
            closure.expired.insert(evidence_id.clone());
            continue;
        }
        for capability_id in record.capability_ids.iter() {
            closure
                .satisfied
                .entry(capability_id.clone())
                .or_default()
                .extend(record.domains.iter().copied());
        }
    }
    for (capability_id, binding) in &manifest.bindings {
        if closure
            .satisfied
            .get(capability_id)
            .is_some_and(|domains| binding.required_evidence.is_subset(domains))
        {
            closure.closed.insert(capability_id.clone());
        }
    }
    closure
}

/// Admits one record only when every freshness identity matches its current subject.
pub fn admit_core_codegen_evidence(
    registry_id: &EvidenceId,
    record: &CoreCodegenEvidenceRecord,
    manifest: &GeneratedBindingManifest,
    policy: &CoreCodegenEvidencePolicy,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    let subject = record.evidence_id.to_string();
    if registry_id != &record.evidence_id {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceSubjectMismatch,
            &subject,
            "evidence map key differs from the embedded identity",
        ));
    }
    if record.target_revision.as_str() != manifest.target_revision
        || record.schema_digest.as_str() != manifest.schema_digest
    {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceTargetMismatch,
            &subject,
            "evidence target or schema digest differs from the binding manifest",
        ));
    }
    if record.subject_revision != policy.subject_revision {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceSubjectMismatch,
            &subject,
            "evidence source revision differs from the reviewed subject revision",
        ));
    }
    if record.projection_fingerprint != manifest.projection_fingerprint {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceSubjectMismatch,
            &subject,
            "evidence projection fingerprint differs from the generated subject",
        ));
    }
    if record.result.outcome != CheckOutcome::Passed {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceOutcomeMissing,
            &subject,
            "only a passing structured result can satisfy a binding",
        ));
    }
    let expected_result_digest = canonical_digest(DigestDomain::Artifact, &record.result).ok();
    if expected_result_digest.as_ref() != Some(&record.result_digest) {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceOutcomeMissing,
            &subject,
            "evidence result digest does not match its structured result",
        ));
    }
    let capability_ids = record.capability_ids.iter().cloned().collect::<Vec<_>>();
    let scope_digest = serde_json::to_vec(&capability_ids)
        .map(Digest::sha256)
        .unwrap_or_else(|_| Digest::sha256([]));
    if record.result.capability_scope_digest != scope_digest {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceObservationInvalid,
            &subject,
            "result scope digest differs from the exact claimed capabilities",
        ));
    }
    if record.capability_ids.is_empty() || record.domains.is_empty() {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceObservationInvalid,
            &subject,
            "evidence requires non-empty capability and domain scopes",
        ));
    }

    let command_digest = canonical_digest(DigestDomain::Artifact, &record.command).ok();
    for domain in &record.domains {
        if command_digest.as_ref() != policy.command_digests.get(domain) {
            diagnostics.push(evidence_diagnostic(
                DiagnosticCode::EvidenceCommandInvalid,
                &subject,
                format!("command identity is not approved for the {domain:?} domain"),
            ));
        }
    }

    let claimed = record
        .capability_ids
        .iter()
        .cloned()
        .collect::<BTreeSet<_>>();
    let fingerprinted = record
        .implementation_fingerprints
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>();
    if claimed != fingerprinted {
        diagnostics.push(evidence_diagnostic(
            DiagnosticCode::EvidenceSubjectMismatch,
            &subject,
            "implementation fingerprints must cover exactly the capability scope",
        ));
    }
    for capability_id in &claimed {
        let Some(binding) = manifest.bindings.get(capability_id) else {
            diagnostics.push(evidence_diagnostic(
                DiagnosticCode::EvidenceObservationInvalid,
                &subject,
                format!("evidence names unknown capability {capability_id}"),
            ));
            continue;
        };
        if record.implementation_fingerprints.get(capability_id)
            != Some(&binding.implementation_fingerprint)
        {
            diagnostics.push(evidence_diagnostic(
                DiagnosticCode::EvidenceSubjectMismatch,
                &subject,
                format!("implementation fingerprint changed for {capability_id}"),
            ));
        }
        if !record.domains.is_subset(&binding.required_evidence) {
            diagnostics.push(evidence_diagnostic(
                DiagnosticCode::EvidenceObservationInvalid,
                &subject,
                format!("evidence claims an undeclared domain for {capability_id}"),
            ));
        }
    }
    diagnostics.finish(())
}

fn evidence_diagnostic(
    code: DiagnosticCode,
    subject: impl ToString,
    detail: impl Into<String>,
) -> ContractDiagnostic {
    ContractDiagnostic::new(code, subject.to_string(), None, detail)
}
