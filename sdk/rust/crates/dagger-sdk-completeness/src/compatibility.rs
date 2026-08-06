//! Evidence-backed engine compatibility claims for Rust SDK releases.
//!
//! Compatibility is validated against complete immutable target descriptors, never version labels
//! alone. Exact claims require evidence for every target; ranged claims require both ordered
//! boundaries. Release metadata is then projected from the validated claim so publication cannot
//! acquire a wider, separately authored compatibility range.

use std::collections::{BTreeMap, BTreeSet};
use std::ops::Deref;

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    CanonicalInventory, CanonicalSet, CheckOutcome, CompatibilityClaim, EvidenceKind,
    EvidenceRegistry, FeatureId, ReleaseCompatibilityMetadata, ResolvedLedger, Status,
    SupportedTargets, TargetDescriptor, TargetDigest,
};

#[derive(Clone, Debug, Eq, PartialEq)]
/// Compatibility claim that passed target, evidence, response, and digest validation.
pub struct ValidatedCompatibilityClaim(CompatibilityClaim);

impl ValidatedCompatibilityClaim {
    /// Borrows the reviewed durable claim.
    pub fn as_inner(&self) -> &CompatibilityClaim {
        &self.0
    }

    /// Derives the only release metadata representation permitted by the contract.
    pub fn release_metadata(&self) -> ReleaseCompatibilityMetadata {
        ReleaseCompatibilityMetadata {
            rust_sdk_version: self.0.rust_sdk_version.clone(),
            supported_targets: self.0.supported_targets.clone(),
            claim_digest: self.0.claim_digest.clone(),
        }
    }
}

impl Deref for ValidatedCompatibilityClaim {
    type Target = CompatibilityClaim;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

/// Validates a release compatibility claim against immutable target and evidence catalogs.
pub fn validate_compatibility_claim(
    claim: CompatibilityClaim,
    targets: &BTreeMap<TargetDigest, TargetDescriptor>,
    evidence: &EvidenceRegistry,
    inventory: &CanonicalInventory,
    ledger: &ResolvedLedger,
) -> Validation<ValidatedCompatibilityClaim> {
    let mut diagnostics = DiagnosticCollector::default();
    validate_target_catalog(targets, &mut diagnostics);

    let required_targets = match &claim.supported_targets {
        SupportedTargets::Exact(exact) => {
            if exact.is_empty() {
                diagnostics.push(compatibility_diagnostic(
                    DiagnosticCode::CompatibilityTargetInvalid,
                    &claim,
                    "exact compatibility target set must not be empty",
                ));
            }
            if claim.range_boundaries != *exact {
                diagnostics.push(compatibility_diagnostic(
                    DiagnosticCode::CompatibilityRangeInvalid,
                    &claim,
                    "exact claim boundaries must equal the exact supported target set",
                ));
            }
            exact.clone()
        }
        SupportedTargets::InclusiveRange(range) => {
            let expected = CanonicalSet::new([range.lower.clone(), range.upper.clone()]);
            if range.lower == range.upper || claim.range_boundaries != expected {
                diagnostics.push(compatibility_diagnostic(
                    DiagnosticCode::CompatibilityRangeInvalid,
                    &claim,
                    "inclusive range requires distinct lower and upper boundary targets",
                ));
            }
            match (targets.get(&range.lower), targets.get(&range.upper)) {
                (Some(lower), Some(upper))
                    if lower.engine_version.version() < upper.engine_version.version() => {}
                (Some(_), Some(_)) => diagnostics.push(compatibility_diagnostic(
                    DiagnosticCode::CompatibilityRangeInvalid,
                    &claim,
                    "inclusive range boundaries are not ordered by their full target versions",
                )),
                _ => {}
            }
            expected
        }
    };

    for target_digest in required_targets.iter() {
        match targets.get(target_digest) {
            Some(target) if target.rust_sdk_version == claim.rust_sdk_version => {}
            Some(_) => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::RustTargetMismatch,
                target_digest.to_string(),
                None,
                "compatibility target assesses a different Rust SDK version",
            )),
            None => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CompatibilityTargetInvalid,
                target_digest.to_string(),
                None,
                "compatibility claim references no validated full target descriptor",
            )),
        }

        let has_passing_evidence = claim.conformance_evidence.iter().any(|evidence_id| {
            evidence.evidence.get(evidence_id).is_some_and(|reference| {
                reference.evidence_kind == EvidenceKind::Verification
                    && reference.execution_target.as_ref() == Some(target_digest)
                    && reference
                        .expected_outcome
                        .as_ref()
                        .is_some_and(|outcome| outcome.outcome == CheckOutcome::Passed)
            })
        });
        if !has_passing_evidence {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CompatibilityEvidenceMissing,
                target_digest.to_string(),
                None,
                "claimed target or range boundary has no passing conformance evidence",
            ));
        }
    }

    let claimed_target_set = required_targets.iter().collect::<BTreeSet<_>>();
    for evidence_id in claim.conformance_evidence.iter() {
        match evidence.evidence.get(evidence_id) {
            Some(reference)
                if reference
                    .execution_target
                    .as_ref()
                    .is_some_and(|target| claimed_target_set.contains(target)) => {}
            Some(_) => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CompatibilityEvidenceMissing,
                evidence_id.to_string(),
                None,
                "compatibility evidence is not scoped to a claimed target or boundary",
            )),
            None => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CompatibilityEvidenceMissing,
                evidence_id.to_string(),
                None,
                "compatibility claim references unknown conformance evidence",
            )),
        }
    }

    validate_outside_range_response(&claim, inventory, ledger, &mut diagnostics);
    let expected_digest = canonical_digest(
        DigestDomain::Compatibility,
        &(
            claim.rust_sdk_version.clone(),
            claim.supported_targets.clone(),
            claim.range_boundaries.clone(),
            claim.conformance_evidence.clone(),
            claim.outside_range_capability.clone(),
        ),
    )
    .expect("validated compatibility inputs must have a canonical digest");
    if claim.claim_digest != expected_digest {
        diagnostics.push(compatibility_diagnostic(
            DiagnosticCode::CompatibilityDrift,
            &claim,
            "claim digest differs from the normalized compatibility inputs",
        ));
    }

    diagnostics.finish(ValidatedCompatibilityClaim(claim))
}

fn validate_target_catalog(
    targets: &BTreeMap<TargetDigest, TargetDescriptor>,
    diagnostics: &mut DiagnosticCollector,
) {
    for (declared_digest, target) in targets {
        let actual = TargetDigest::new(
            canonical_digest(DigestDomain::Target, target)
                .expect("validated TargetDescriptor must have a canonical target digest"),
        );
        if declared_digest != &actual {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CompatibilityTargetInvalid,
                declared_digest.to_string(),
                None,
                "target catalog key differs from the full descriptor digest",
            ));
        }
    }
}

fn validate_outside_range_response(
    claim: &CompatibilityClaim,
    inventory: &CanonicalInventory,
    ledger: &ResolvedLedger,
    diagnostics: &mut DiagnosticCollector,
) {
    if !inventory
        .capabilities
        .contains_key(&claim.outside_range_capability)
    {
        diagnostics.push(compatibility_diagnostic(
            DiagnosticCode::CompatibilityResponseMissing,
            claim,
            "typed outside-range response is absent from the canonical inventory",
        ));
        return;
    }
    let Some(record) = ledger.capabilities.get(&claim.outside_range_capability) else {
        diagnostics.push(compatibility_diagnostic(
            DiagnosticCode::CompatibilityResponseMissing,
            claim,
            "typed outside-range response has no explicit ledger classification",
        ));
        return;
    };
    if matches!(record.status, Status::Missing | Status::Partial)
        && !matches!(
            record.owner_feature,
            Some(FeatureId::Feature2 | FeatureId::Feature3)
        )
    {
        diagnostics.push(compatibility_diagnostic(
            DiagnosticCode::CompatibilityResponseMissing,
            claim,
            "incomplete outside-range response must be owned by Feature 2 or Feature 3",
        ));
    }
}

fn compatibility_diagnostic(
    code: DiagnosticCode,
    claim: &CompatibilityClaim,
    detail: impl Into<String>,
) -> ContractDiagnostic {
    ContractDiagnostic::new(code, claim.rust_sdk_version.to_string(), None, detail)
}
