//! Exact ledger expansion and truthful status-entry validation.
//!
//! Classification rules are intentionally a small conjunction-only language over canonical
//! inventory attributes. A rule may make a large reviewed ledger readable, but its expected set
//! remains a fail-closed fence: inventory drift can never classify a newly matching capability
//! without a human-reviewed artifact change.

use std::collections::{BTreeMap, BTreeSet};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    CanonicalInventory, CanonicalSet, CapabilityDefinition, CapabilityId, CapabilityRecord,
    CheckOutcome, ClassificationInput, ClassificationRule, ClassificationSelector,
    ClassificationValues, EvidenceKind, EvidenceReference, EvidenceRegistry, ExpectedSet,
    ResolvedLedger, SourceItemInventory, Status,
};

/// Expands exact rows and fenced rules into exactly one row per canonical capability.
pub fn resolve_classifications(
    inventory: &CanonicalInventory,
    source_items: &SourceItemInventory,
    input: &ClassificationInput,
) -> Validation<ResolvedLedger> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut classified = BTreeMap::<CapabilityId, ClassificationValues>::new();

    for (capability_id, values) in &input.exact {
        if !inventory.capabilities.contains_key(capability_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::ClassificationOverrideInvalid,
                capability_id.to_string(),
                None,
                "exact classification names no active capability",
            ));
            continue;
        }
        classified.insert(capability_id.clone(), values.clone());
    }

    let mut seen_rule_ids = BTreeSet::new();
    for (map_rule_id, rule) in &input.rules {
        if map_rule_id != &rule.rule_id || !seen_rule_ids.insert(rule.rule_id.clone()) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::ClassificationRuleDuplicate,
                rule.rule_id.to_string(),
                None,
                "classification rule map key and embedded identity must agree exactly once",
            ));
        }
        if rule
            .selector
            .authority_id
            .as_ref()
            .is_some_and(|authority| authority != &rule.authority_id)
        {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::ClassificationSelectorInvalid,
                rule.rule_id.to_string(),
                None,
                "selector authority cannot widen or contradict the rule authority boundary",
            ));
        }

        let expansion = CanonicalSet::new(
            inventory
                .capabilities
                .values()
                .filter(|definition| {
                    definition.authority_id == rule.authority_id
                        && selector_matches(&rule.selector, definition, source_items)
                })
                .map(|definition| definition.capability_id.clone()),
        );
        validate_expected_expansion(rule, &expansion, &mut diagnostics);

        for override_id in rule.overrides.keys() {
            if !expansion.contains(override_id) {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::ClassificationOverrideInvalid,
                    override_id.to_string(),
                    None,
                    format!("override is stale or outside rule {}", rule.rule_id),
                ));
            }
        }

        for capability_id in expansion.as_slice() {
            let values = rule
                .overrides
                .get(capability_id)
                .unwrap_or(&rule.classification)
                .clone();
            if classified.insert(capability_id.clone(), values).is_some() {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::CapabilityDuplicate,
                    capability_id.to_string(),
                    None,
                    "capability is classified by more than one exact row or rule",
                ));
            }
        }
    }

    for capability_id in inventory.capabilities.keys() {
        if !classified.contains_key(capability_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityStatusInvalid,
                capability_id.to_string(),
                None,
                "active capability has no exact classification or rule expansion",
            ));
        }
    }

    let capabilities = inventory
        .capabilities
        .iter()
        .filter_map(|(capability_id, definition)| {
            classified
                .get(capability_id)
                .map(|values| (capability_id.clone(), resolved_record(definition, values)))
        })
        .collect();
    diagnostics.finish(ResolvedLedger { capabilities })
}

fn selector_matches(
    selector: &ClassificationSelector,
    definition: &CapabilityDefinition,
    source_items: &SourceItemInventory,
) -> bool {
    selector
        .authority_id
        .as_ref()
        .is_none_or(|value| value == &definition.authority_id)
        && selector
            .capability_kind
            .as_ref()
            .is_none_or(|value| value == &definition.capability_kind)
        && selector
            .stability
            .as_ref()
            .is_none_or(|value| value == &definition.stability)
        && selector.source_item_kind.as_ref().is_none_or(|kind| {
            definition.source_item_ids.iter().any(|source_item_id| {
                source_items
                    .items
                    .get(source_item_id)
                    .is_some_and(|item| &item.item_kind == kind)
            })
        })
        && selector
            .capability_id_prefix
            .as_ref()
            .is_none_or(|prefix| id_is_within_prefix(&definition.capability_id, prefix))
}

fn id_is_within_prefix(capability_id: &CapabilityId, prefix: &CapabilityId) -> bool {
    capability_id == prefix
        || capability_id
            .as_str()
            .strip_prefix(prefix.as_str())
            .is_some_and(|suffix| suffix.starts_with('/'))
}

fn validate_expected_expansion(
    rule: &ClassificationRule,
    expansion: &CanonicalSet<CapabilityId>,
    diagnostics: &mut DiagnosticCollector,
) {
    let matches = match &rule.expected_capability_ids {
        ExpectedSet::CapabilityIds(expected) => expected == expansion,
        ExpectedSet::Digest(expected) => canonical_digest(DigestDomain::RuleExpansion, expansion)
            .is_ok_and(|actual| &actual == expected),
    };
    if !matches {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::LedgerDrift,
            rule.rule_id.to_string(),
            None,
            "current rule expansion differs from its reviewed ordered set or digest",
        ));
    }
}

fn resolved_record(
    definition: &CapabilityDefinition,
    values: &ClassificationValues,
) -> CapabilityRecord {
    CapabilityRecord {
        capability_id: definition.capability_id.clone(),
        authority_id: definition.authority_id.clone(),
        capability_kind: definition.capability_kind.clone(),
        source_item_ids: definition.source_item_ids.clone(),
        source_anchors: definition.source_anchors.clone(),
        summary: definition.summary.clone(),
        semantic_signature: definition.semantic_signature.clone(),
        capability_fingerprint: definition.capability_fingerprint.clone(),
        status: values.status.clone(),
        stability: definition.stability.clone(),
        gap: values.gap.clone(),
        owner_feature: values.owner_feature.clone(),
        implementation_evidence: values.implementation_evidence.clone(),
        verification_evidence: values.verification_evidence.clone(),
        decision_evidence: values.decision_evidence.clone(),
    }
}

/// Enforces the five reviewed status shapes and evidence-kind links for every ledger row.
pub fn validate_status_entries(
    ledger: &ResolvedLedger,
    evidence: &EvidenceRegistry,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    for (map_id, capability) in &ledger.capabilities {
        if map_id != &capability.capability_id {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityDuplicate,
                capability.capability_id.to_string(),
                None,
                "ledger map key and embedded capability identity differ",
            ));
        }
        if capability.source_anchors.is_empty() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                capability.capability_id.to_string(),
                None,
                "every status requires pinned authority anchors",
            ));
        }
        for anchor in capability.source_anchors.as_slice() {
            if anchor.evidence_kind != EvidenceKind::Authority
                || !anchor
                    .proved_capability_ids
                    .contains(&capability.capability_id)
            {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::CapabilitySourceMissing,
                    capability.capability_id.to_string(),
                    Some(anchor.locator.clone()),
                    "source anchor must be authority evidence scoped back to this capability",
                ));
            }
        }

        let has_gap = capability.gap.is_some();
        let has_owner = capability.owner_feature.is_some();
        let has_implementation = !capability.implementation_evidence.is_empty();
        let has_verification = !capability.verification_evidence.is_empty();
        let has_decision = !capability.decision_evidence.is_empty();

        match capability.status {
            Status::Missing => {
                require_gap_owner(capability, has_gap, has_owner, &mut diagnostics);
                if has_implementation || has_verification {
                    diagnostics.push(status_shape_diagnostic(
                        capability,
                        "Missing cannot carry implementation or verification evidence",
                    ));
                }
            }
            Status::Partial => {
                require_gap_owner(capability, has_gap, has_owner, &mut diagnostics);
                if !has_implementation {
                    diagnostics.push(ContractDiagnostic::new(
                        DiagnosticCode::ImplementationEvidenceMissing,
                        capability.capability_id.to_string(),
                        None,
                        "Partial requires Rust implementation evidence",
                    ));
                }
            }
            Status::Implemented => {
                forbid_gap_owner(capability, has_gap, has_owner, &mut diagnostics);
                require_complete_evidence(
                    capability,
                    has_implementation,
                    has_verification,
                    &mut diagnostics,
                );
            }
            Status::IdiomaticEquivalent => {
                forbid_gap_owner(capability, has_gap, has_owner, &mut diagnostics);
                require_complete_evidence(
                    capability,
                    has_implementation,
                    has_verification,
                    &mut diagnostics,
                );
                if !has_decision {
                    diagnostics.push(ContractDiagnostic::new(
                        DiagnosticCode::DecisionEvidenceInvalid,
                        capability.capability_id.to_string(),
                        None,
                        "Idiomatic_Equivalent requires a reviewed Rust mapping decision",
                    ));
                }
            }
            Status::Inapplicable => {
                forbid_gap_owner(capability, has_gap, has_owner, &mut diagnostics);
                if has_implementation || has_verification {
                    diagnostics.push(status_shape_diagnostic(
                        capability,
                        "Inapplicable cannot carry implementation or verification evidence",
                    ));
                }
                if !has_decision {
                    diagnostics.push(ContractDiagnostic::new(
                        DiagnosticCode::DecisionEvidenceInvalid,
                        capability.capability_id.to_string(),
                        None,
                        "Inapplicable requires a reviewed no-counterpart decision",
                    ));
                }
            }
        }

        validate_evidence_links(capability, evidence, &mut diagnostics);
    }
    diagnostics.finish(())
}

fn require_gap_owner(
    capability: &CapabilityRecord,
    has_gap: bool,
    has_owner: bool,
    diagnostics: &mut DiagnosticCollector,
) {
    if !has_gap {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityGapInvalid,
            capability.capability_id.to_string(),
            None,
            "blocking status requires an exact residual gap",
        ));
    }
    if !has_owner {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityOwnerMissing,
            capability.capability_id.to_string(),
            None,
            "blocking status requires exactly one Feature 2-9 owner",
        ));
    }
}

fn forbid_gap_owner(
    capability: &CapabilityRecord,
    has_gap: bool,
    has_owner: bool,
    diagnostics: &mut DiagnosticCollector,
) {
    if has_gap {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityGapInvalid,
            capability.capability_id.to_string(),
            None,
            "complete status cannot retain a blocking gap",
        ));
    }
    if has_owner {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityStatusInvalid,
            capability.capability_id.to_string(),
            None,
            "complete status cannot retain a blocking-work owner",
        ));
    }
}

fn require_complete_evidence(
    capability: &CapabilityRecord,
    has_implementation: bool,
    has_verification: bool,
    diagnostics: &mut DiagnosticCollector,
) {
    if !has_implementation {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::ImplementationEvidenceMissing,
            capability.capability_id.to_string(),
            None,
            "complete status requires Rust implementation evidence",
        ));
    }
    if !has_verification {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::VerificationEvidenceMissing,
            capability.capability_id.to_string(),
            None,
            "complete status requires executable passing verification evidence",
        ));
    }
}

fn validate_evidence_links(
    capability: &CapabilityRecord,
    registry: &EvidenceRegistry,
    diagnostics: &mut DiagnosticCollector,
) {
    validate_slot(
        capability,
        &capability.implementation_evidence,
        EvidenceKind::Implementation,
        DiagnosticCode::ImplementationEvidenceMissing,
        registry,
        diagnostics,
    );
    validate_slot(
        capability,
        &capability.verification_evidence,
        EvidenceKind::Verification,
        DiagnosticCode::VerificationEvidenceMissing,
        registry,
        diagnostics,
    );
    validate_slot(
        capability,
        &capability.decision_evidence,
        EvidenceKind::Decision,
        DiagnosticCode::DecisionEvidenceInvalid,
        registry,
        diagnostics,
    );
}

fn validate_slot(
    capability: &CapabilityRecord,
    ids: &CanonicalSet<crate::model::EvidenceId>,
    expected_kind: EvidenceKind,
    code: DiagnosticCode,
    registry: &EvidenceRegistry,
    diagnostics: &mut DiagnosticCollector,
) {
    for id in ids.as_slice() {
        let Some(reference) = registry.evidence.get(id) else {
            diagnostics.push(ContractDiagnostic::new(
                code,
                capability.capability_id.to_string(),
                None,
                format!("evidence {id} is absent from the candidate registry"),
            ));
            continue;
        };
        if reference.evidence_id != *id
            || reference.evidence_kind != expected_kind
            || !reference
                .proved_capability_ids
                .contains(&capability.capability_id)
            || (expected_kind == EvidenceKind::Verification
                && !is_executable_passing_verification(reference))
            || (expected_kind == EvidenceKind::Decision
                && capability.status == Status::Inapplicable
                && !proves_no_meaningful_rust_counterpart(reference))
        {
            diagnostics.push(ContractDiagnostic::new(
                code,
                capability.capability_id.to_string(),
                Some(reference.locator.clone()),
                format!("evidence {id} does not satisfy this status-entry slot"),
            ));
        }
    }
}

fn is_executable_passing_verification(reference: &EvidenceReference) -> bool {
    reference.command.is_some()
        && reference
            .expected_outcome
            .as_ref()
            .is_some_and(|outcome| outcome.outcome == CheckOutcome::Passed)
        && reference.execution_target.is_some()
        && !reference.platform_scope.is_empty()
        && !reference.path.as_str().ends_with(".md")
}

fn proves_no_meaningful_rust_counterpart(reference: &EvidenceReference) -> bool {
    let claim = reference.claim.as_str().to_ascii_lowercase();
    claim.contains("no meaningful rust counterpart")
        || claim.contains("language-specific with no rust counterpart")
}

fn status_shape_diagnostic(capability: &CapabilityRecord, detail: &str) -> ContractDiagnostic {
    ContractDiagnostic::new(
        DiagnosticCode::CapabilityStatusInvalid,
        capability.capability_id.to_string(),
        None,
        detail,
    )
}
