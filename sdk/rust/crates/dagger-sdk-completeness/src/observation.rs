//! Machine-readable transport observations and exact-target admission policy.
//!
//! Deterministic fixtures and live engine runs are different evidence classes. This
//! module keeps that distinction structural: a fixture record has no field capable of
//! carrying live-run facts, while a live record is accepted only when its exact target,
//! authenticated request, explicit close, and child reaping all agree with the pinned
//! contract. Capability scope is checked in both directions against the evidence
//! registry so a passing command cannot silently claim an unobserved row.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    CanonicalSet, CapabilityId, CheckId, CheckOutcome, CommitSha, DaggerVersion, EvidenceId,
    EvidenceKind, EvidenceRegistry, Platform, ResolvedLedger, SemverVersion, Status,
    TargetDescriptor, TargetDigest,
};

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Side-effect boundary asserted by a transport evidence fixture.
pub enum TransportObservationKind {
    /// Process inputs select exactly one authoritative connection source.
    Source,
    /// Release metadata and bytes are acquired through the constrained origin.
    Acquisition,
    /// Cache validation, publication, leasing, and retention preserve integrity.
    Cache,
    /// Native CLI arguments, environment, labels, and retry policy are enforced.
    Launch,
    /// The bounded control record is isolated before resource transfer.
    Protocol,
    /// Loopback authentication and at-most-once request delivery are enforced.
    Http,
    /// W3C context reaches child and request carriers without global mutation.
    Propagation,
    /// Runtime identity is compared with the exact compiled target.
    Compatibility,
    /// Public failures preserve typed and redacted semantic coordinates.
    ErrorMapping,
    /// Explicit close is bounded, exhaustive, repeatable, and reaps owned children.
    Shutdown,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Whether an observation executed a Dagger engine.
pub enum TransportObservationMode {
    /// Portable fixtures and reference models executed no engine.
    DeterministicFixture,
    /// The stable connector downloaded, started, queried, and closed the exact target.
    ExactTargetLive,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// One test assertion and the exact capabilities it observed.
pub struct TransportAssertion {
    pub kind: TransportObservationKind,
    pub check_id: CheckId,
    pub capability_ids: CanonicalSet<CapabilityId>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Reproducible facts emitted only by the isolated live connector test.
pub struct ExactTargetRun {
    pub rust_sdk_version: SemverVersion,
    pub cli_version: DaggerVersion,
    pub observed_engine_version: DaggerVersion,
    pub dagger_revision: CommitSha,
    pub sdk_started_session: bool,
    pub authenticated_query: bool,
    pub propagation_observed: bool,
    pub diagnostic_boundary_observed: bool,
    pub explicit_close: bool,
    pub child_reaped: bool,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Versioned observation set supporting one verification evidence record.
pub struct TransportObservationRecord {
    pub format_version: SemverVersion,
    pub evidence_id: EvidenceId,
    pub mode: TransportObservationMode,
    pub target: TargetDigest,
    pub platform_scope: CanonicalSet<Platform>,
    pub assertions: Vec<TransportAssertion>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub exact_target_run: Option<ExactTargetRun>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Transport observation records keyed by their verification evidence identity.
pub struct TransportObservationRegistry {
    pub observations: BTreeMap<EvidenceId, TransportObservationRecord>,
}

/// Validates deterministic and live observations against the pinned target and scope.
pub fn validate_transport_observations(
    registry: &TransportObservationRegistry,
    evidence: &EvidenceRegistry,
    target: &TargetDescriptor,
    target_digest: &TargetDigest,
    capability_scope: &CanonicalSet<CapabilityId>,
    ledger: &ResolvedLedger,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut deterministic_scope = BTreeSet::new();
    let mut live_kinds = BTreeSet::new();
    let mut accepted_live = false;

    for (map_id, record) in &registry.observations {
        let subject = record.evidence_id.to_string();
        if map_id != &record.evidence_id {
            reject(
                &mut diagnostics,
                &subject,
                "observation map key and identity differ",
            );
        }
        if record.format_version != target.contract_format_version {
            reject(&mut diagnostics, &subject, "observation format is stale");
        }
        if &record.target != target_digest {
            reject(
                &mut diagnostics,
                &subject,
                "observation target differs from the exact contract target",
            );
        }
        if record.assertions.is_empty() || record.platform_scope.is_empty() {
            reject(
                &mut diagnostics,
                &subject,
                "observation requires assertions and a portable platform scope",
            );
        }

        let mut observed_scope = BTreeSet::new();
        let mut kinds = BTreeSet::new();
        for assertion in &record.assertions {
            if assertion.capability_ids.is_empty() {
                reject(
                    &mut diagnostics,
                    &subject,
                    "an observation assertion has empty capability scope",
                );
            }
            kinds.insert(assertion.kind.clone());
            for capability_id in assertion.capability_ids.iter() {
                if !capability_scope.contains(capability_id) {
                    reject(
                        &mut diagnostics,
                        &subject,
                        "an observation claims a capability outside the reviewed transport scope",
                    );
                }
                observed_scope.insert(capability_id.clone());
            }
        }

        let Some(reference) = evidence.evidence.get(map_id) else {
            reject(
                &mut diagnostics,
                &subject,
                "observation has no evidence registry record",
            );
            continue;
        };
        if reference.evidence_kind != EvidenceKind::Verification
            || reference.execution_target.as_ref() != Some(target_digest)
            || reference
                .expected_outcome
                .as_ref()
                .map(|outcome| &outcome.outcome)
                != Some(&CheckOutcome::Passed)
            || reference.command.is_none()
            || reference.platform_scope != record.platform_scope
            || reference
                .proved_capability_ids
                .iter()
                .cloned()
                .collect::<BTreeSet<_>>()
                != observed_scope
        {
            reject(
                &mut diagnostics,
                &subject,
                "observation and executable verification record do not agree exactly",
            );
        }

        match record.mode {
            TransportObservationMode::DeterministicFixture => {
                if record.exact_target_run.is_some() {
                    reject(
                        &mut diagnostics,
                        &subject,
                        "deterministic evidence cannot carry or claim live-run facts",
                    );
                }
                deterministic_scope.extend(observed_scope);
            }
            TransportObservationMode::ExactTargetLive => {
                live_kinds.extend(kinds);
                let record_accepted = record
                    .exact_target_run
                    .as_ref()
                    .is_some_and(|run| exact_run_matches(run, target));
                accepted_live |= record_accepted;
                if !record_accepted {
                    reject(
                        &mut diagnostics,
                        &subject,
                        "live evidence lacks one or more exact-target lifecycle facts",
                    );
                }
            }
        }
    }

    if deterministic_scope != capability_scope.iter().cloned().collect() {
        reject(
            &mut diagnostics,
            "transport/deterministic-scope",
            "deterministic observations must cover exactly the 58 reviewed transport candidates",
        );
    }
    let required_live = BTreeSet::from([
        TransportObservationKind::Source,
        TransportObservationKind::Acquisition,
        TransportObservationKind::Launch,
        TransportObservationKind::Protocol,
        TransportObservationKind::Http,
        TransportObservationKind::Propagation,
        TransportObservationKind::Compatibility,
        TransportObservationKind::Shutdown,
    ]);
    let completed_scope = capability_scope
        .iter()
        .filter(|capability_id| {
            ledger.capabilities.get(*capability_id).is_some_and(|row| {
                matches!(
                    row.status,
                    Status::Implemented | Status::IdiomaticEquivalent | Status::Inapplicable
                )
            })
        })
        .count();
    if completed_scope > 0
        && (completed_scope != capability_scope.len()
            || !accepted_live
            || !required_live.is_subset(&live_kinds))
    {
        reject(
            &mut diagnostics,
            "transport/exact-target-live",
            "transport completion is atomic and requires an exact-target connector run through authenticated query and child reaping",
        );
    }

    diagnostics.finish(())
}

fn exact_run_matches(run: &ExactTargetRun, target: &TargetDescriptor) -> bool {
    let observed = run.observed_engine_version.version();
    let expected = target.engine_version.version();
    let revision_prefix = &target.dagger_revision.as_str()[..8];
    run.rust_sdk_version == target.rust_sdk_version
        && run.cli_version == target.engine_version
        && observed.major == expected.major
        && observed.minor == expected.minor
        && observed.patch == expected.patch
        && observed.pre == expected.pre
        && observed.build.as_str().split('.').next() == Some(revision_prefix)
        && run.dagger_revision == target.dagger_revision
        && run.sdk_started_session
        && run.authenticated_query
        && run.propagation_observed
        && run.diagnostic_boundary_observed
        && run.explicit_close
        && run.child_reaped
}

fn reject(diagnostics: &mut DiagnosticCollector, subject: &str, detail: &str) {
    diagnostics.push(ContractDiagnostic::new(
        DiagnosticCode::EvidenceObservationInvalid,
        subject,
        None,
        detail,
    ));
}
