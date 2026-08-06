//! Feature-scope declarations and candidate ledger-change traceability.
//!
//! Downstream work cannot close anonymous capabilities. The narrow Markdown reader binds an
//! approved feature specification to an exact, digest-fenced scope, while the candidate validators
//! preserve unrelated ledger facts and require destination-status evidence in the same change.

use std::collections::BTreeMap;

use crate::classification::validate_status_entries;
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    CanonicalInventory, CanonicalSet, CapabilityId, CapabilityRecord, ClassificationValues, Digest,
    EvidenceKind, EvidenceRegistry, FeatureId, NonEmptyText, ResolvedLedger, Status, TargetDigest,
};

const EXISTING_SCOPE_HEADING: &str =
    "### Existing Capability_IDs Whose Status Feature 2 Intends to Change";
const POLICY_SCOPE_HEADING: &str = "### Omitted Policy_Capabilities to Add and Complete";
const EXPECTED_EXISTING_SCOPE_DIGEST: &str =
    "sha256:81ad1a4f2efe1604b9091468bd6a6006d598a2a8ae54a94a974acf08d74b8b40";
const EXPECTED_EXISTING_SCOPE_LEN: usize = 23;
const EXPECTED_POLICY_IDS: [&str; 14] = [
    "policy/rust-policy/client-beta-config-migration",
    "policy/rust-policy/client-cancelled-connect-cleanup",
    "policy/rust-policy/client-close-idempotency",
    "policy/rust-policy/client-closed-operation-rejection",
    "policy/rust-policy/client-drop-cleanup",
    "policy/rust-policy/client-http-connect-timeout",
    "policy/rust-policy/client-owned-lifecycle",
    "policy/rust-policy/client-preflight-validation",
    "policy/rust-policy/client-public-surface-encapsulation",
    "policy/rust-policy/client-query-execution-timeout",
    "policy/rust-policy/client-reserved-environment",
    "policy/rust-policy/client-secret-redaction",
    "policy/rust-policy/client-session-startup-timeout",
    "policy/rust-policy/client-shared-handle-safety",
];

#[derive(Clone, Debug, Eq, PartialEq)]
/// Exact existing and newly inventoried capabilities declared by one approved feature spec.
pub struct FeatureScopeDeclaration {
    /// Feature which owns the declared work.
    pub feature: FeatureId,
    /// Existing ledger identities whose status the feature may change.
    pub existing_capability_ids: CanonicalSet<CapabilityId>,
    /// Direct SHA-256 digest of the compact JSON encoding of the ordered existing IDs.
    pub existing_scope_digest: Digest,
    /// New policy identities which the feature adds to the canonical inventory.
    pub policy_capability_ids: CanonicalSet<CapabilityId>,
}

impl FeatureScopeDeclaration {
    /// Returns the complete declared capability scope in canonical order.
    pub fn capability_ids(&self) -> CanonicalSet<CapabilityId> {
        CanonicalSet::new(
            self.existing_capability_ids
                .iter()
                .chain(self.policy_capability_ids.iter())
                .cloned(),
        )
    }
}

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

#[derive(Clone, Debug, Eq, PartialEq)]
/// Exact sibling-feature dependency which prevents a local capability from becoming complete.
pub struct ResidualBlocker {
    /// Sibling feature whose observable semantics remain unverified.
    pub sibling_feature: FeatureId,
    /// Exact residual gap which the candidate blocking row must retain.
    pub gap: NonEmptyText,
}

/// Parses and validates the exact Feature 2 scope convention in `requirements.md`.
///
/// The reader intentionally understands only the two named headings, their `text` fences, and the
/// one recorded digest. This keeps prose from becoming an accidental second configuration format.
pub fn parse_feature_scope_declaration(markdown: &str) -> Validation<FeatureScopeDeclaration> {
    let mut diagnostics = DiagnosticCollector::default();
    let existing_lines = parse_id_fence(markdown, EXISTING_SCOPE_HEADING, &mut diagnostics);
    let policy_lines = parse_id_fence(markdown, POLICY_SCOPE_HEADING, &mut diagnostics);
    let existing = parse_capability_ids(EXISTING_SCOPE_HEADING, &existing_lines, &mut diagnostics);
    let policies = parse_capability_ids(POLICY_SCOPE_HEADING, &policy_lines, &mut diagnostics);

    validate_order_and_duplicates(
        EXISTING_SCOPE_HEADING,
        &existing_lines,
        &existing,
        &mut diagnostics,
    );
    validate_order_and_duplicates(
        POLICY_SCOPE_HEADING,
        &policy_lines,
        &policies,
        &mut diagnostics,
    );

    let calculated_digest = serde_json::to_vec(&existing_lines)
        .map(Digest::sha256)
        .unwrap_or_else(|_| Digest::sha256([]));
    let recorded_digest =
        parse_recorded_digest(markdown, &mut diagnostics).unwrap_or_else(|| Digest::sha256([]));
    let expected_digest = Digest::new(EXPECTED_EXISTING_SCOPE_DIGEST)
        .expect("the reviewed Feature 2 scope digest is a valid SHA-256 identity");
    if existing.len() != EXPECTED_EXISTING_SCOPE_LEN
        || calculated_digest != expected_digest
        || recorded_digest != expected_digest
    {
        diagnostics.push(scope_diagnostic(
            EXISTING_SCOPE_HEADING,
            "existing Feature 2 scope must contain the reviewed 23 IDs and recorded digest",
        ));
    }

    let expected_policies = CanonicalSet::new(
        EXPECTED_POLICY_IDS
            .into_iter()
            .map(|id| CapabilityId::new(id).expect("reviewed policy identity is valid")),
    );
    let policies = CanonicalSet::new(policies);
    if policies != expected_policies {
        diagnostics.push(scope_diagnostic(
            POLICY_SCOPE_HEADING,
            "Feature 2 policy scope differs from the reviewed 14 identities",
        ));
    }

    diagnostics.finish(FeatureScopeDeclaration {
        feature: FeatureId::Feature2,
        existing_capability_ids: CanonicalSet::new(existing),
        existing_scope_digest: recorded_digest,
        policy_capability_ids: policies,
    })
}

/// Validates that the canonical inventory and blocking-owner projection retain the declared scope.
pub fn validate_feature_scope_routing(
    inventory: &CanonicalInventory,
    ledger: &ResolvedLedger,
    declaration: &FeatureScopeDeclaration,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    let scope = declaration.capability_ids();
    for capability_id in declaration.existing_capability_ids.iter() {
        match inventory.capabilities.get(capability_id) {
            Some(definition) if definition.authority_id.as_str() == "go-client" => {}
            _ => diagnostics.push(scope_diagnostic(
                capability_id,
                "declared existing Feature 2 identity is absent from the Go-client inventory",
            )),
        }
    }
    for capability_id in declaration.policy_capability_ids.iter() {
        match inventory.capabilities.get(capability_id) {
            Some(definition) if definition.authority_id.as_str() == "rust-policy" => {}
            _ => diagnostics.push(scope_diagnostic(
                capability_id,
                "declared Feature 2 policy identity is absent from the Rust-policy inventory",
            )),
        }
    }

    for row in ledger.capabilities.values() {
        let is_blocking = matches!(row.status, Status::Missing | Status::Partial);
        if scope.contains(&row.capability_id) {
            if is_blocking && row.owner_feature != Some(declaration.feature.clone()) {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::CapabilityOwnerMissing,
                    row.capability_id.to_string(),
                    None,
                    "declared blocking Feature 2 capability has a different owner",
                ));
            }
        } else if row.owner_feature == Some(declaration.feature.clone()) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityOwnerMissing,
                row.capability_id.to_string(),
                None,
                "capability outside the declared Feature 2 scope retains Feature 2 ownership",
            ));
        }
    }
    diagnostics.finish(())
}

/// Proves that a routing-only correction changed no status, identity, anchor, or evidence field.
pub fn validate_ownership_only_correction(
    before: &ResolvedLedger,
    after: &ResolvedLedger,
    declaration: &FeatureScopeDeclaration,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    let scope = declaration.capability_ids();
    for (capability_id, before_row) in &before.capabilities {
        let Some(after_row) = after.capabilities.get(capability_id) else {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "ownership correction removed a baseline capability",
            ));
            continue;
        };
        if scope.contains(capability_id) {
            continue;
        }
        if !preserved_routing_projection(before_row, after_row) {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "ownership correction changed a field other than owner or blocking gap",
            ));
        }
    }
    for capability_id in after.capabilities.keys() {
        if !before.capabilities.contains_key(capability_id)
            && !declaration.policy_capability_ids.contains(capability_id)
        {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "ownership correction introduced an undeclared capability",
            ));
        }
    }
    diagnostics.finish(())
}

/// Validates Feature 2 status changes, same-target evidence, and exact sibling blockers.
///
/// `require_no_blockers` is the final-gate mode: every declared row must be complete and the
/// sibling-blocker set must be empty. Earlier candidate changes may legitimately retain local or
/// cross-feature blocking work.
#[allow(clippy::too_many_arguments)]
pub fn validate_feature_status_changes(
    current: &ResolvedLedger,
    declaration: &FeatureScopeDeclaration,
    candidate: &CandidateStatusChanges,
    candidate_evidence: &EvidenceRegistry,
    target: &TargetDigest,
    residual_blockers: &BTreeMap<CapabilityId, ResidualBlocker>,
    require_no_blockers: bool,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    let child = ChildSpecDeclaration {
        feature: declaration.feature.clone(),
        capability_ids: declaration.capability_ids(),
    };
    if let Err(errors) =
        validate_downstream_traceability(current, &child, candidate, candidate_evidence)
    {
        diagnostics.extend(errors.into_inner());
    }

    for (capability_id, replacement) in &candidate.changes {
        if replacement.status.is_complete() {
            validate_complete_evidence(
                capability_id,
                replacement,
                candidate_evidence,
                target,
                &mut diagnostics,
            );
            if residual_blockers.contains_key(capability_id) {
                diagnostics.push(scope_diagnostic(
                    capability_id,
                    "complete Feature 2 status cannot erase an unverified sibling dependency",
                ));
            }
        }
    }

    for (capability_id, blocker) in residual_blockers {
        if !child.capability_ids.contains(capability_id)
            || !matches!(
                blocker.sibling_feature,
                FeatureId::Feature3
                    | FeatureId::Feature4
                    | FeatureId::Feature8
                    | FeatureId::Feature9
            )
        {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "residual blocker must name a declared capability and Feature 3, 4, 8, or 9",
            ));
            continue;
        }
        let values = candidate.changes.get(capability_id).cloned().or_else(|| {
            current
                .capabilities
                .get(capability_id)
                .map(classification_of)
        });
        let Some(values) = values else {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "residual blocker names no current or candidate capability",
            ));
            continue;
        };
        if !matches!(values.status, Status::Missing | Status::Partial)
            || values.owner_feature != Some(declaration.feature.clone())
            || values.gap.as_ref() != Some(&blocker.gap)
        {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "candidate must retain the exact sibling residual as a Feature 2 blocking row",
            ));
        }
    }

    if require_no_blockers {
        if !residual_blockers.is_empty() {
            diagnostics.push(scope_diagnostic(
                "feature-2",
                "final Feature 2 validation cannot retain sibling blockers",
            ));
        }
        for capability_id in child.capability_ids.iter() {
            let status = candidate
                .changes
                .get(capability_id)
                .map(|values| &values.status)
                .or_else(|| {
                    current
                        .capabilities
                        .get(capability_id)
                        .map(|row| &row.status)
                });
            if !status.is_some_and(Status::is_complete) {
                diagnostics.push(scope_diagnostic(
                    capability_id,
                    "final Feature 2 validation requires every declared capability to be complete",
                ));
            }
        }
    }
    diagnostics.finish(())
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

fn parse_id_fence(
    markdown: &str,
    heading: &str,
    diagnostics: &mut DiagnosticCollector,
) -> Vec<String> {
    let heading_count = markdown.lines().filter(|line| *line == heading).count();
    let Some(section) = markdown.split_once(heading).map(|(_, tail)| {
        tail.split_once("\n### ")
            .map_or(tail, |(section, _)| section)
    }) else {
        diagnostics.push(scope_diagnostic(
            heading,
            "required scope heading is absent",
        ));
        return Vec::new();
    };
    if heading_count != 1 {
        diagnostics.push(scope_diagnostic(
            heading,
            "required scope heading must occur exactly once",
        ));
    }
    let fences = section.match_indices("```text\n").collect::<Vec<_>>();
    if fences.len() != 1 {
        diagnostics.push(scope_diagnostic(
            heading,
            "scope section must contain exactly one text fence",
        ));
        return Vec::new();
    }
    let fenced = &section[fences[0].0 + "```text\n".len()..];
    let Some((body, remainder)) = fenced.split_once("\n```") else {
        diagnostics.push(scope_diagnostic(heading, "scope text fence is not closed"));
        return Vec::new();
    };
    if remainder.contains("```") || body.is_empty() {
        diagnostics.push(scope_diagnostic(
            heading,
            "scope section contains an empty or additional fenced value",
        ));
    }
    body.lines().map(str::to_owned).collect()
}

fn parse_recorded_digest(markdown: &str, diagnostics: &mut DiagnosticCollector) -> Option<Digest> {
    let section = markdown.split_once(EXISTING_SCOPE_HEADING)?.1;
    let section = section
        .split_once("\n### ")
        .map_or(section, |(section, _)| section);
    let matches = section
        .split('`')
        .filter(|value| value.starts_with("sha256:"))
        .collect::<Vec<_>>();
    if matches.len() != 1 {
        diagnostics.push(scope_diagnostic(
            EXISTING_SCOPE_HEADING,
            "existing scope section must record exactly one SHA-256 digest",
        ));
        return None;
    }
    match Digest::new(matches[0]) {
        Ok(digest) => Some(digest),
        Err(_) => {
            diagnostics.push(scope_diagnostic(
                EXISTING_SCOPE_HEADING,
                "recorded existing scope digest is malformed",
            ));
            None
        }
    }
}

fn parse_capability_ids(
    subject: &str,
    lines: &[String],
    diagnostics: &mut DiagnosticCollector,
) -> Vec<CapabilityId> {
    lines
        .iter()
        .filter_map(|line| match CapabilityId::new(line) {
            Ok(id) => Some(id),
            Err(_) => {
                diagnostics.push(scope_diagnostic(
                    subject,
                    "scope fence contains a malformed Capability_ID",
                ));
                None
            }
        })
        .collect()
}

fn validate_order_and_duplicates(
    subject: &str,
    lines: &[String],
    ids: &[CapabilityId],
    diagnostics: &mut DiagnosticCollector,
) {
    let canonical = CanonicalSet::new(ids.iter().cloned());
    let canonical_lines = canonical
        .iter()
        .map(ToString::to_string)
        .collect::<Vec<_>>();
    if canonical.len() != ids.len() || canonical_lines != lines {
        diagnostics.push(scope_diagnostic(
            subject,
            "scope IDs must be unique and lexicographically ordered",
        ));
    }
}

fn validate_complete_evidence(
    capability_id: &CapabilityId,
    replacement: &ClassificationValues,
    evidence: &EvidenceRegistry,
    target: &TargetDigest,
    diagnostics: &mut DiagnosticCollector,
) {
    let slots = match replacement.status {
        Status::Implemented | Status::IdiomaticEquivalent => vec![
            (
                &replacement.implementation_evidence,
                EvidenceKind::Implementation,
            ),
            (
                &replacement.verification_evidence,
                EvidenceKind::Verification,
            ),
        ],
        // Inapplicable is complete under the F1 status policy, but implementation and execution
        // evidence would be contradictory. Its reviewed decision is therefore the target-scoped
        // proof required at this boundary.
        Status::Inapplicable => vec![(&replacement.decision_evidence, EvidenceKind::Decision)],
        Status::Missing | Status::Partial => return,
    };
    for (ids, expected_kind) in slots {
        for id in ids.iter() {
            let valid = evidence.evidence.get(id).is_some_and(|reference| {
                reference.evidence_kind == expected_kind
                    && reference.execution_target.as_ref() == Some(target)
                    && reference.proved_capability_ids.contains(capability_id)
            });
            if !valid {
                diagnostics.push(ContractDiagnostic::new(
                    match expected_kind {
                        EvidenceKind::Verification => DiagnosticCode::VerificationEvidenceMissing,
                        EvidenceKind::Decision => DiagnosticCode::DecisionEvidenceInvalid,
                        EvidenceKind::Implementation | EvidenceKind::Authority => {
                            DiagnosticCode::ImplementationEvidenceMissing
                        }
                    },
                    capability_id.to_string(),
                    None,
                    "complete Feature 2 evidence is absent from the candidate target scope",
                ));
            }
        }
    }
}

fn preserved_routing_projection(before: &CapabilityRecord, after: &CapabilityRecord) -> bool {
    before.capability_id == after.capability_id
        && before.authority_id == after.authority_id
        && before.capability_kind == after.capability_kind
        && before.source_item_ids == after.source_item_ids
        && before.source_anchors == after.source_anchors
        && before.summary == after.summary
        && before.semantic_signature == after.semantic_signature
        && before.capability_fingerprint == after.capability_fingerprint
        && before.status == after.status
        && before.stability == after.stability
        && before.implementation_evidence == after.implementation_evidence
        && before.verification_evidence == after.verification_evidence
        && before.decision_evidence == after.decision_evidence
}

fn classification_of(row: &CapabilityRecord) -> ClassificationValues {
    ClassificationValues {
        status: row.status.clone(),
        gap: row.gap.clone(),
        owner_feature: row.owner_feature.clone(),
        implementation_evidence: row.implementation_evidence.clone(),
        verification_evidence: row.verification_evidence.clone(),
        decision_evidence: row.decision_evidence.clone(),
    }
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

fn scope_diagnostic(subject: impl ToString, detail: impl Into<String>) -> ContractDiagnostic {
    ContractDiagnostic::new(
        DiagnosticCode::CapabilityStatusInvalid,
        subject.to_string(),
        None,
        detail,
    )
}

trait CompleteStatus {
    fn is_complete(&self) -> bool;
}

impl CompleteStatus for Status {
    fn is_complete(&self) -> bool {
        matches!(
            self,
            Status::Implemented | Status::IdiomaticEquivalent | Status::Inapplicable
        )
    }
}
