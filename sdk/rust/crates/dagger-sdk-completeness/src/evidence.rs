//! Immutable evidence provenance, executable scope, and reverse-row auditing.
//!
//! Evidence is admitted by what its pinned locator can prove, not by its filename or proximity to
//! implementation. The source registry therefore records eligibility explicitly; documentation,
//! issues, pull requests, source-only anchors, removed tests, and harness-self checks remain useful
//! audit history without becoming passing verification.

use std::collections::{BTreeMap, BTreeSet};
use std::ops::Deref;

use crate::command::{CommandPolicy, command_defects};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    AuthorityRegistry, CanonicalInventory, CheckOutcome, CommitSha, EvidenceKind,
    EvidenceReference, EvidenceRegistry, RepositoryId, RepositoryRelativePath, ResolvedLedger,
    SourceItemState, SourceLocator, TargetDigest,
};

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
/// Whether a pinned source locator may support executable verification.
pub enum EvidenceEligibility {
    ExecutableAssertion,
    SourceOnly,
    Documentation,
    Issue,
    PullRequest,
    HarnessSelf,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
/// One exact locator available to the evidence auditor at an immutable revision.
pub struct EvidenceSource {
    pub repository: RepositoryId,
    pub revision: CommitSha,
    pub path: RepositoryRelativePath,
    pub locator: SourceLocator,
    pub state: SourceItemState,
    pub eligibility: EvidenceEligibility,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Closed set of pinned paths and locators available to evidence references.
pub struct EvidenceSourceRegistry {
    sources: BTreeSet<EvidenceSource>,
}

impl EvidenceSourceRegistry {
    /// Constructs an exact, duplicate-free evidence source registry.
    pub fn new(sources: impl IntoIterator<Item = EvidenceSource>) -> Self {
        Self {
            sources: sources.into_iter().collect(),
        }
    }

    /// Borrows the registered sources in deterministic order.
    pub fn sources(&self) -> &BTreeSet<EvidenceSource> {
        &self.sources
    }
}

/// Immutable context against which evidence references are audited.
pub struct EvidenceAuditContext<'a> {
    pub authorities: &'a AuthorityRegistry,
    pub sources: &'a EvidenceSourceRegistry,
    pub inventory: &'a CanonicalInventory,
    pub target: &'a TargetDigest,
    pub command_policy: &'a CommandPolicy,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Registry whose provenance, kind-specific shape, and row scope are internally consistent.
pub struct ValidatedEvidenceRegistry(EvidenceRegistry);

impl Deref for ValidatedEvidenceRegistry {
    type Target = EvidenceRegistry;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

/// Audits all evidence records and their exact bidirectional relationship with ledger rows.
pub fn audit_evidence_registry(
    registry: EvidenceRegistry,
    ledger: &ResolvedLedger,
    context: &EvidenceAuditContext<'_>,
) -> Validation<ValidatedEvidenceRegistry> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut authority_anchors = BTreeMap::new();
    for row in ledger.capabilities.values() {
        for anchor in row.source_anchors.as_slice() {
            if anchor.evidence_kind != EvidenceKind::Authority {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::EvidenceKindInvalid,
                    anchor.evidence_id.to_string(),
                    Some(anchor.locator.clone()),
                    "capability source anchors must use authority evidence",
                ));
            }
            if authority_anchors
                .insert(anchor.evidence_id.clone(), anchor.clone())
                .is_some_and(|previous| previous != *anchor)
            {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::EvidenceKindInvalid,
                    anchor.evidence_id.to_string(),
                    Some(anchor.locator.clone()),
                    "one authority evidence identity has conflicting durable references",
                ));
            }
        }
    }
    for anchor in authority_anchors.values() {
        audit_reference(anchor, context, &mut diagnostics);
        audit_reverse_scope(anchor, ledger, &mut diagnostics);
    }
    for (map_id, evidence) in &registry.evidence {
        if map_id != &evidence.evidence_id {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::EvidenceKindInvalid,
                evidence.evidence_id.to_string(),
                Some(evidence.locator.clone()),
                "evidence map key and embedded identity differ",
            ));
        }
        audit_reference(evidence, context, &mut diagnostics);
        audit_reverse_scope(evidence, ledger, &mut diagnostics);
    }
    audit_ledger_links(ledger, &registry, &mut diagnostics);
    diagnostics.finish(ValidatedEvidenceRegistry(registry))
}

fn audit_reference(
    evidence: &EvidenceReference,
    context: &EvidenceAuditContext<'_>,
    diagnostics: &mut DiagnosticCollector,
) {
    let matching_authority = context.authorities.authorities.values().find(|authority| {
        authority.repository == evidence.repository && authority.revision == evidence.revision
    });
    if matching_authority.is_none() {
        let repository_registered = context
            .authorities
            .authorities
            .values()
            .any(|authority| authority.repository == evidence.repository);
        diagnostics.push(ContractDiagnostic::new(
            if repository_registered {
                DiagnosticCode::EvidenceRevisionMismatch
            } else {
                DiagnosticCode::EvidenceRepositoryInvalid
            },
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "evidence repository and immutable revision must match one registered authority",
        ));
    }

    let path_exists = context.sources.sources().iter().any(|source| {
        source.repository == evidence.repository
            && source.revision == evidence.revision
            && source.path == evidence.path
    });
    let exact_source = context.sources.sources().iter().find(|source| {
        source.repository == evidence.repository
            && source.revision == evidence.revision
            && source.path == evidence.path
            && source.locator == evidence.locator
    });
    if !path_exists {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidencePathInvalid,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "evidence path is not contained in the pinned source registry",
        ));
    } else if exact_source.is_none() {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidenceLocatorInvalid,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "evidence locator does not exist at the pinned path and revision",
        ));
    }

    if evidence.proved_capability_ids.is_empty()
        || evidence
            .proved_capability_ids
            .iter()
            .any(|id| !context.inventory.capabilities.contains_key(id))
    {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidenceOutcomeMissing,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "evidence must name a non-empty exact set of active capabilities",
        ));
    }

    match evidence.evidence_kind {
        EvidenceKind::Verification => {
            audit_verification(evidence, exact_source, context, diagnostics);
        }
        EvidenceKind::Authority | EvidenceKind::Implementation | EvidenceKind::Decision => {
            if evidence.command.is_some() || evidence.expected_outcome.is_some() {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::EvidenceCommandInvalid,
                    evidence.evidence_id.to_string(),
                    Some(evidence.locator.clone()),
                    "only verification evidence may carry an executable command and outcome",
                ));
            }
            if !evidence.platform_scope.is_empty() {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::EvidencePlatformInvalid,
                    evidence.evidence_id.to_string(),
                    Some(evidence.locator.clone()),
                    "non-verification evidence cannot claim an observed platform scope",
                ));
            }
        }
    }
}

fn audit_verification(
    evidence: &EvidenceReference,
    exact_source: Option<&EvidenceSource>,
    context: &EvidenceAuditContext<'_>,
    diagnostics: &mut DiagnosticCollector,
) {
    if exact_source.is_some_and(|source| {
        source.state == SourceItemState::Skipped
            || source.state == SourceItemState::Removed
            || source.state == SourceItemState::HarnessSelf
            || source.eligibility != EvidenceEligibility::ExecutableAssertion
    }) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidenceOutcomeMissing,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "this locator is audit history or source context, not passing executable evidence",
        ));
    }

    match &evidence.command {
        Some(command) => {
            for detail in command_defects(command, context.command_policy) {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::EvidenceCommandInvalid,
                    evidence.evidence_id.to_string(),
                    Some(evidence.locator.clone()),
                    detail,
                ));
            }
        }
        None => diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidenceCommandInvalid,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "verification evidence requires a reproducible argv command",
        )),
    }
    if !evidence
        .expected_outcome
        .as_ref()
        .is_some_and(|outcome| outcome.outcome == CheckOutcome::Passed)
    {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidenceOutcomeMissing,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "verification completion requires a recorded passing assertion",
        ));
    }
    if evidence.execution_target.as_ref() != Some(context.target) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidenceTargetMismatch,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "verification target differs from the candidate contract target",
        ));
    }
    if evidence.platform_scope.is_empty() {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::EvidencePlatformInvalid,
            evidence.evidence_id.to_string(),
            Some(evidence.locator.clone()),
            "verification evidence requires an exact non-empty platform scope",
        ));
    }
}

fn audit_reverse_scope(
    evidence: &EvidenceReference,
    ledger: &ResolvedLedger,
    diagnostics: &mut DiagnosticCollector,
) {
    for capability_id in evidence.proved_capability_ids.as_slice() {
        let Some(row) = ledger.capabilities.get(capability_id) else {
            continue;
        };
        let linked = match evidence.evidence_kind {
            EvidenceKind::Authority => row
                .source_anchors
                .iter()
                .any(|anchor| anchor.evidence_id == evidence.evidence_id),
            EvidenceKind::Implementation => {
                row.implementation_evidence.contains(&evidence.evidence_id)
            }
            EvidenceKind::Verification => row.verification_evidence.contains(&evidence.evidence_id),
            EvidenceKind::Decision => row.decision_evidence.contains(&evidence.evidence_id),
        };
        if !linked {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::EvidenceOutcomeMissing,
                evidence.evidence_id.to_string(),
                Some(evidence.locator.clone()),
                format!("proved capability {capability_id} does not link back to this evidence"),
            ));
        }
    }
}

fn audit_ledger_links(
    ledger: &ResolvedLedger,
    registry: &EvidenceRegistry,
    diagnostics: &mut DiagnosticCollector,
) {
    for row in ledger.capabilities.values() {
        for (evidence_id, expected_kind) in row
            .implementation_evidence
            .iter()
            .map(|id| (id, EvidenceKind::Implementation))
            .chain(
                row.verification_evidence
                    .iter()
                    .map(|id| (id, EvidenceKind::Verification)),
            )
            .chain(
                row.decision_evidence
                    .iter()
                    .map(|id| (id, EvidenceKind::Decision)),
            )
        {
            if !registry.evidence.get(evidence_id).is_some_and(|evidence| {
                evidence.evidence_kind == expected_kind
                    && evidence.proved_capability_ids.contains(&row.capability_id)
            }) {
                diagnostics.push(ContractDiagnostic::new(
                    match expected_kind {
                        EvidenceKind::Implementation => {
                            DiagnosticCode::ImplementationEvidenceMissing
                        }
                        EvidenceKind::Verification => DiagnosticCode::VerificationEvidenceMissing,
                        EvidenceKind::Decision => DiagnosticCode::DecisionEvidenceInvalid,
                        EvidenceKind::Authority => DiagnosticCode::EvidenceKindInvalid,
                    },
                    row.capability_id.to_string(),
                    None,
                    format!("ledger evidence {evidence_id} lacks an exact reverse scope"),
                ));
            }
        }
    }
}
