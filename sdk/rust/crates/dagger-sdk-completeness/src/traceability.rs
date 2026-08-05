//! Child-spec declarations and candidate ledger-change traceability.
//!
//! A downstream feature cannot close anonymous work. Every proposed status change must be named in
//! its child specification and must carry the evidence required by its destination status in the
//! same candidate contract.

use std::collections::BTreeMap;

use crate::classification::validate_status_entries;
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    CanonicalSet, CapabilityId, CapabilityRecord, ClassificationValues, EvidenceRegistry,
    FeatureId, ResolvedLedger,
};

#[derive(Clone, Debug, Eq, PartialEq)]
/// Capability identities a child feature declares an intent to change.
pub struct ChildSpecDeclaration {
    pub feature: FeatureId,
    pub capability_ids: CanonicalSet<CapabilityId>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Proposed replacement classifications keyed by current Capability_ID.
pub struct CandidateStatusChanges {
    pub changes: BTreeMap<CapabilityId, ClassificationValues>,
}

/// Validates declared IDs, actual transitions, ownership, and candidate-local evidence.
pub fn validate_downstream_traceability(
    current: &ResolvedLedger,
    declaration: &ChildSpecDeclaration,
    candidate: &CandidateStatusChanges,
    candidate_evidence: &EvidenceRegistry,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    for capability_id in declaration.capability_ids.as_slice() {
        if !current.capabilities.contains_key(capability_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityStatusInvalid,
                capability_id.to_string(),
                None,
                "child specification cites no current Capability_ID",
            ));
        }
    }

    let mut changed_rows = BTreeMap::new();
    for (capability_id, replacement) in &candidate.changes {
        let Some(current_row) = current.capabilities.get(capability_id) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityStatusInvalid,
                capability_id.to_string(),
                None,
                "candidate status change names no current Capability_ID",
            ));
            continue;
        };
        if !declaration.capability_ids.contains(capability_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityStatusInvalid,
                capability_id.to_string(),
                None,
                "candidate changes a capability not cited by the child specification",
            ));
        }
        if replacement.status == current_row.status {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityStatusInvalid,
                capability_id.to_string(),
                None,
                "candidate status entry does not change the current status",
            ));
        }
        if replacement
            .owner_feature
            .as_ref()
            .is_some_and(|owner| owner != &declaration.feature)
        {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityOwnerMissing,
                capability_id.to_string(),
                None,
                "candidate blocking owner differs from its child feature",
            ));
        }
        changed_rows.insert(
            capability_id.clone(),
            replace_classification(current_row, replacement),
        );
    }

    if let Err(errors) = validate_status_entries(
        &ResolvedLedger {
            capabilities: changed_rows,
        },
        candidate_evidence,
    ) {
        diagnostics.extend(errors.into_inner());
    }
    diagnostics.finish(())
}

fn replace_classification(
    current: &CapabilityRecord,
    replacement: &ClassificationValues,
) -> CapabilityRecord {
    let mut row = current.clone();
    row.status = replacement.status.clone();
    row.gap = replacement.gap.clone();
    row.owner_feature = replacement.owner_feature.clone();
    row.implementation_evidence = replacement.implementation_evidence.clone();
    row.verification_evidence = replacement.verification_evidence.clone();
    row.decision_evidence = replacement.decision_evidence.clone();
    row
}
