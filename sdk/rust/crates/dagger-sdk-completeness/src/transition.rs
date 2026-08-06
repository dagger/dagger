//! Semantic target transitions and reviewed Rust API impact.
//!
//! A target diff reports facts derived from immutable contract snapshots; a separate review says
//! what changed Rust API means for SemVer and migration. Keeping those inputs separate prevents a
//! reviewer from editing an added/removed set to fit a desired release classification. Removed
//! rows retain the complete prior record, while evidence attached to changed rows must be bound to
//! the successor target before it can remain eligible.

use std::collections::{BTreeMap, BTreeSet};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    AuthorityChange, AuthorityRegistry, CanonicalInventory, CanonicalSet, CapabilityChange,
    CapabilityId, EvidenceId, EvidenceRegistry, FeatureId, HarnessCheckChange, HarnessMappings,
    HistoricalCapabilityRecord, ResolvedLedger, RustApiChangeKind, RustApiTransitionReview,
    SemverEffect, SpecReference, Stability, TargetDescriptor, TargetDigest, TargetTransition,
};

#[derive(Clone, Copy, Debug)]
/// Immutable contract data required to compare one assessed target.
///
/// Callers must supply artifacts that have already passed their component validators. This layer
/// validates only cross-target facts and never reinterprets either snapshot's authority data.
pub struct ContractSnapshot<'a> {
    pub target: &'a TargetDescriptor,
    pub authorities: &'a AuthorityRegistry,
    pub inventory: &'a CanonicalInventory,
    pub ledger: &'a ResolvedLedger,
    pub evidence: &'a EvidenceRegistry,
    pub harness_mappings: &'a HarnessMappings,
}

/// Derives and validates the complete semantic transition between two contract snapshots.
///
/// Every affected stable, experimental, or internal Rust capability requires exactly one review.
/// Schema-only and otherwise not-applicable capabilities remain visible in the structural diff but
/// cannot inflate the Rust API SemVer effect.
pub fn diff_targets(
    from: ContractSnapshot<'_>,
    to: ContractSnapshot<'_>,
    reviews: &CanonicalSet<RustApiTransitionReview>,
) -> Validation<TargetTransition> {
    let mut diagnostics = DiagnosticCollector::default();
    let from_target = target_digest(from.target);
    let to_target = target_digest(to.target);

    if to.target.previous_target.as_ref() != Some(&from_target) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::TransitionBaseInvalid,
            to_target.to_string(),
            None,
            "successor target does not identify the compared target as previous_target",
        ));
    }

    let from_ids = from
        .inventory
        .capabilities
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>();
    let to_ids = to
        .inventory
        .capabilities
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>();
    let added_capabilities = CanonicalSet::new(to_ids.difference(&from_ids).cloned());
    let removed_ids = from_ids.difference(&to_ids).cloned().collect::<Vec<_>>();

    let mut removed_capabilities = Vec::new();
    for capability_id in &removed_ids {
        match from.ledger.capabilities.get(capability_id) {
            Some(capability) => removed_capabilities.push(HistoricalCapabilityRecord {
                target: from_target.clone(),
                capability: capability.clone(),
            }),
            None => diagnostics.push(transition_diff_diagnostic(
                capability_id,
                "removed capability has no prior ledger row to preserve",
            )),
        }
    }
    removed_capabilities.sort_unstable_by(|left, right| {
        left.capability
            .capability_id
            .cmp(&right.capability.capability_id)
    });

    let changed_capabilities =
        CanonicalSet::new(from_ids.intersection(&to_ids).filter_map(|capability_id| {
            let old = &from.inventory.capabilities[capability_id];
            let new = &to.inventory.capabilities[capability_id];
            (old.capability_fingerprint != new.capability_fingerprint).then(|| CapabilityChange {
                capability_id: capability_id.clone(),
                from_fingerprint: old.capability_fingerprint.clone(),
                to_fingerprint: new.capability_fingerprint.clone(),
            })
        }));

    validate_candidate_rows(
        &added_capabilities,
        &changed_capabilities,
        from,
        to,
        &to_target,
        &mut diagnostics,
    );
    let authority_changes = diff_authorities(from.authorities, to.authorities, &mut diagnostics);
    let harness_changes = diff_harness(from.harness_mappings, to.harness_mappings);
    let (semver_effect, migration_requirements) = validate_stability_reviews(
        &added_capabilities,
        &removed_ids,
        &changed_capabilities,
        from.inventory,
        to.inventory,
        reviews,
        &mut diagnostics,
    );

    diagnostics.finish(TargetTransition {
        from_target,
        to_target: to.target.clone(),
        added_capabilities,
        removed_capabilities,
        changed_capabilities,
        authority_changes,
        harness_changes,
        semver_effect,
        migration_requirements,
    })
}

fn target_digest(target: &TargetDescriptor) -> TargetDigest {
    // TargetDescriptor contains only serializable scalar/map forms. Encoding failure would mean a
    // crate invariant was broken, not that a caller supplied an invalid transition.
    TargetDigest::new(
        canonical_digest(DigestDomain::Target, target)
            .expect("validated TargetDescriptor must have a canonical target digest"),
    )
}

fn transition_diff_diagnostic(
    capability_id: &CapabilityId,
    detail: impl Into<String>,
) -> ContractDiagnostic {
    ContractDiagnostic::new(
        DiagnosticCode::TransitionDiffInvalid,
        capability_id.to_string(),
        None,
        detail,
    )
}

fn validate_candidate_rows(
    added: &CanonicalSet<CapabilityId>,
    changed: &CanonicalSet<CapabilityChange>,
    from: ContractSnapshot<'_>,
    to: ContractSnapshot<'_>,
    to_target: &TargetDigest,
    diagnostics: &mut DiagnosticCollector,
) {
    for capability_id in added.iter() {
        let Some(record) = to.ledger.capabilities.get(capability_id) else {
            diagnostics.push(transition_diff_diagnostic(
                capability_id,
                "added capability requires an explicit successor classification",
            ));
            continue;
        };
        if record.capability_fingerprint
            != to.inventory.capabilities[capability_id].capability_fingerprint
        {
            diagnostics.push(transition_diff_diagnostic(
                capability_id,
                "added capability classification does not describe the successor fingerprint",
            ));
        }
    }

    for change in changed.iter() {
        let capability_id = &change.capability_id;
        let Some(record) = to.ledger.capabilities.get(capability_id) else {
            diagnostics.push(transition_diff_diagnostic(
                capability_id,
                "changed capability requires a successor classification",
            ));
            continue;
        };
        if record.capability_fingerprint != change.to_fingerprint {
            diagnostics.push(transition_diff_diagnostic(
                capability_id,
                "changed capability classification retains the prior fingerprint",
            ));
        }

        let prior = from.ledger.capabilities.get(capability_id);
        for evidence_id in record
            .implementation_evidence
            .iter()
            .chain(record.verification_evidence.iter())
        {
            if !evidence_is_revalidated(evidence_id, from.evidence, to.evidence, to_target, prior) {
                diagnostics.push(transition_diff_diagnostic(
                    capability_id,
                    format!(
                        "changed capability evidence {evidence_id} is not revalidated for the successor target"
                    ),
                ));
            }
        }
    }
}

fn evidence_is_revalidated(
    evidence_id: &EvidenceId,
    from: &EvidenceRegistry,
    to: &EvidenceRegistry,
    to_target: &TargetDigest,
    prior: Option<&crate::model::CapabilityRecord>,
) -> bool {
    let Some(candidate) = to.evidence.get(evidence_id) else {
        return false;
    };
    if candidate.execution_target.as_ref() != Some(to_target) {
        return false;
    }

    let was_previously_claimed = prior.is_some_and(|record| {
        record.implementation_evidence.contains(evidence_id)
            || record.verification_evidence.contains(evidence_id)
    });
    // A stable Evidence_ID may survive a target transition, but its durable record must change to
    // bind the new target. Merely copying the old registry would silently preserve stale proof.
    !was_previously_claimed || from.evidence.get(evidence_id) != Some(candidate)
}

fn diff_authorities(
    from: &AuthorityRegistry,
    to: &AuthorityRegistry,
    diagnostics: &mut DiagnosticCollector,
) -> CanonicalSet<AuthorityChange> {
    let from_ids = from.authorities.keys().collect::<BTreeSet<_>>();
    let to_ids = to.authorities.keys().collect::<BTreeSet<_>>();
    if from_ids != to_ids {
        for authority_id in from_ids.symmetric_difference(&to_ids) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::TransitionDiffInvalid,
                authority_id.to_string(),
                None,
                "authority addition or removal cannot be represented as a source-digest change",
            ));
        }
    }

    CanonicalSet::new(from_ids.intersection(&to_ids).filter_map(|authority_id| {
        let old = &from.authorities[*authority_id];
        let new = &to.authorities[*authority_id];
        (old.source_digest != new.source_digest).then(|| AuthorityChange {
            authority_id: (*authority_id).clone(),
            from_source_digest: old.source_digest.clone(),
            to_source_digest: new.source_digest.clone(),
        })
    }))
}

fn diff_harness(from: &HarnessMappings, to: &HarnessMappings) -> CanonicalSet<HarnessCheckChange> {
    let check_ids = from
        .checks
        .keys()
        .chain(to.checks.keys())
        .cloned()
        .collect::<BTreeSet<_>>();
    CanonicalSet::new(check_ids.into_iter().filter_map(|check_id| {
        let old = from
            .checks
            .get(&check_id)
            .map(|check| check.source_fingerprint.clone());
        let new = to
            .checks
            .get(&check_id)
            .map(|check| check.source_fingerprint.clone());
        (old != new).then_some(HarnessCheckChange {
            check_id,
            from_fingerprint: old,
            to_fingerprint: new,
        })
    }))
}

#[allow(clippy::too_many_arguments)]
fn validate_stability_reviews(
    added: &CanonicalSet<CapabilityId>,
    removed: &[CapabilityId],
    changed: &CanonicalSet<CapabilityChange>,
    from: &CanonicalInventory,
    to: &CanonicalInventory,
    reviews: &CanonicalSet<RustApiTransitionReview>,
    diagnostics: &mut DiagnosticCollector,
) -> (SemverEffect, CanonicalSet<SpecReference>) {
    let affected = added
        .iter()
        .cloned()
        .chain(removed.iter().cloned())
        .chain(changed.iter().map(|change| change.capability_id.clone()))
        .collect::<BTreeSet<_>>();
    let reviewable = affected
        .iter()
        .filter(|capability_id| {
            let old = from.capabilities.get(*capability_id);
            let new = to.capabilities.get(*capability_id);
            old.into_iter()
                .chain(new)
                .any(|definition| definition.stability != Stability::NotApplicable)
        })
        .cloned()
        .collect::<BTreeSet<_>>();
    let by_capability = reviews
        .iter()
        .map(|review| (review.capability_id.clone(), review))
        .collect::<BTreeMap<_, _>>();
    if by_capability.len() != reviews.len() {
        let mut seen = BTreeSet::new();
        for review in reviews.iter() {
            if !seen.insert(review.capability_id.clone()) {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::TransitionSemverInvalid,
                    review.capability_id.to_string(),
                    None,
                    "affected Rust API capability has more than one stability review",
                ));
            }
        }
    }

    for review in reviews.iter() {
        if !reviewable.contains(&review.capability_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::TransitionSemverInvalid,
                review.capability_id.to_string(),
                None,
                "Rust API review does not correspond to an affected reviewable capability",
            ));
        }
    }

    let mut effect = SemverEffect::None;
    let mut migrations = Vec::new();
    for capability_id in reviewable {
        let Some(review) = by_capability.get(&capability_id).copied() else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::TransitionSemverInvalid,
                capability_id.to_string(),
                None,
                "affected Rust API capability has no stability review",
            ));
            continue;
        };
        let expected_structural_kind = if added.contains(&capability_id) {
            Some(RustApiChangeKind::Added)
        } else if removed.contains(&capability_id) {
            Some(RustApiChangeKind::Removed)
        } else {
            None
        };
        if expected_structural_kind
            .as_ref()
            .is_some_and(|expected| expected != &review.change_kind)
            || (expected_structural_kind.is_none()
                && matches!(
                    review.change_kind,
                    RustApiChangeKind::Added | RustApiChangeKind::Removed
                ))
        {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::TransitionSemverInvalid,
                capability_id.to_string(),
                None,
                "reviewed API change kind contradicts the structural target diff",
            ));
        }

        let old_stability = from
            .capabilities
            .get(&capability_id)
            .map(|definition| &definition.stability);
        let new_stability = to
            .capabilities
            .get(&capability_id)
            .map(|definition| &definition.stability);
        let experimental = old_stability == Some(&Stability::Experimental)
            || new_stability == Some(&Stability::Experimental);
        if experimental && review.experimental_condition.is_none() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityStabilityInvalid,
                capability_id.to_string(),
                None,
                "experimental API transition requires a graduation or removal condition",
            ));
        }

        let stable =
            old_stability == Some(&Stability::Stable) || new_stability == Some(&Stability::Stable);
        let candidate_effect = match review.change_kind {
            RustApiChangeKind::Added
                if new_stability != Some(&Stability::Internal)
                    && new_stability != Some(&Stability::NotApplicable) =>
            {
                SemverEffect::Additive
            }
            RustApiChangeKind::Deprecated if stable => SemverEffect::Deprecation,
            RustApiChangeKind::Removed | RustApiChangeKind::Incompatible if stable => {
                SemverEffect::Breaking
            }
            _ => SemverEffect::None,
        };
        effect = maximum_effect(effect, candidate_effect);

        let migration_required = review.user_facing
            && matches!(
                review.change_kind,
                RustApiChangeKind::Removed | RustApiChangeKind::Incompatible
            );
        match (&review.migration_requirement, migration_required) {
            (Some(requirement), true) if requirement.owner_feature == FeatureId::Feature9 => {
                migrations.push(requirement.reference.clone());
            }
            (Some(_), true) | (None, true) => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::TransitionMigrationMissing,
                capability_id.to_string(),
                None,
                "user-facing incompatibility requires a Feature 9 migration requirement",
            )),
            (Some(_), false) => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::TransitionMigrationMissing,
                capability_id.to_string(),
                None,
                "migration requirements are allowed only for user-facing incompatibilities",
            )),
            (None, false) => {}
        }
    }

    (effect, CanonicalSet::new(migrations))
}

fn maximum_effect(left: SemverEffect, right: SemverEffect) -> SemverEffect {
    fn rank(effect: &SemverEffect) -> u8 {
        match effect {
            SemverEffect::None => 0,
            SemverEffect::Additive => 1,
            SemverEffect::Deprecation => 2,
            SemverEffect::Breaking => 3,
        }
    }
    if rank(&right) > rank(&left) {
        right
    } else {
        left
    }
}

/// Returns deterministic drift diagnostics when semantic inputs move without a reviewed
/// transition.
///
/// The caller intentionally supplies a successful transition result: this function translates
/// every represented change into the stable `LEDGER_DRIFT` diagnostics used by normal verify.
pub fn drift_diagnostics(transition: &TargetTransition) -> Vec<ContractDiagnostic> {
    let mut diagnostics = Vec::new();
    for capability_id in transition.added_capabilities.iter() {
        diagnostics.push(ledger_drift(capability_id.to_string(), "capability added"));
    }
    for removed in &transition.removed_capabilities {
        diagnostics.push(ledger_drift(
            removed.capability.capability_id.to_string(),
            "capability removed",
        ));
    }
    for changed in transition.changed_capabilities.iter() {
        diagnostics.push(ledger_drift(
            changed.capability_id.to_string(),
            "capability fingerprint changed",
        ));
    }
    for authority in transition.authority_changes.iter() {
        diagnostics.push(ledger_drift(
            authority.authority_id.to_string(),
            "authority source digest changed",
        ));
    }
    for check in transition.harness_changes.iter() {
        diagnostics.push(ledger_drift(
            check.check_id.to_string(),
            "harness check fingerprint changed",
        ));
    }
    diagnostics.sort_unstable();
    diagnostics
}

fn ledger_drift(subject: String, detail: &'static str) -> ContractDiagnostic {
    ContractDiagnostic::new(DiagnosticCode::LedgerDrift, subject, None, detail)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn semver_effect_order_is_release_conservative() {
        assert_eq!(
            maximum_effect(SemverEffect::Additive, SemverEffect::Deprecation),
            SemverEffect::Deprecation
        );
        assert_eq!(
            maximum_effect(SemverEffect::Breaking, SemverEffect::None),
            SemverEffect::Breaking
        );
    }
}
