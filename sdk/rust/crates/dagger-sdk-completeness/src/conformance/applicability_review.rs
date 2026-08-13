//! Exact-ID authoring and deterministic expansion for applicability review.
//!
//! Groups reduce repetition in the reviewed source artifact, but they carry only explicit
//! capability sets. Expansion joins every identity back to its own ledger row so grouping cannot
//! inherit an anchor, fingerprint, or status from another capability.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    CanonicalSet, CapabilityId, Digest, RepositoryRelativePath, ResolvedLedger, SourceLocator,
    Status, TargetDigest,
};

use super::applicability::{authority_anchor, is_blocking};
use super::{
    ApplicabilityDecision, ApplicabilityDisposition, ApplicabilityGroupId, ApplicabilityRecord,
    AssertionId, ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, ConformanceScope, ConformanceScopeInput, DiagnosticCoordinate,
    DiagnosticPhase, ReviewedConformanceScope, SignoffCaseId, derive_conformance_scope,
    reviewed_policy_capabilities, validate_existing_conformance_scope,
};

/// Checked exact-ID source from which the complete applicability artifact is expanded.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApplicabilityReviewInput {
    pub format_version: ConformanceFormatVersion,
    pub target_digest: TargetDigest,
    pub existing_scope_digest: Digest,
    pub groups: Vec<ApplicabilityReviewGroup>,
}

/// One authored decision shared only by the explicit capability identities in this group.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApplicabilityReviewGroup {
    pub group_id: ApplicabilityGroupId,
    pub capability_ids: CanonicalSet<CapabilityId>,
    pub decision: ApplicabilityGroupDecision,
    pub routing: ApplicabilityGroupRouting,
    pub terminal_policy: Status,
}

/// Closed decision templates which expand into capability-local evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum ApplicabilityGroupDecision {
    SameMechanism,
    IdiomaticEquivalence {
        observable_contract: SourceLocator,
        rust_mechanism: SourceLocator,
    },
    EngineOwnedNoRustObligation,
    ForeignSdkNoRustObligation {
        foreign_mechanism: SourceLocator,
    },
}

/// Explicit shared routes or deterministic per-capability route namespaces.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum ApplicabilityGroupRouting {
    None,
    PerCapability {
        assertion_namespace: AssertionId,
        #[serde(skip_serializing_if = "Option::is_none")]
        case_namespace: Option<SignoffCaseId>,
    },
    Shared {
        assertion_ids: CanonicalSet<AssertionId>,
        case_ids: CanonicalSet<SignoffCaseId>,
    },
}

/// Review output derived from the expanded records without changing ledger status.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApplicabilityAudit {
    pub format_version: ConformanceFormatVersion,
    pub target_digest: TargetDigest,
    pub existing_scope_digest: Digest,
    pub admitted_scope_digest: Digest,
    pub review_digest: Digest,
    pub record_digest: Digest,
    pub group_count: u32,
    pub existing_record_count: u32,
    pub source_counts: BTreeMap<RepositoryRelativePath, u32>,
    pub disposition_counts: BTreeMap<ApplicabilityDisposition, u32>,
    pub assertion_sharing: BTreeMap<AssertionId, u32>,
    pub terminal_policy_counts: BTreeMap<Status, u32>,
    pub current_blocker_count: u32,
    pub projected_terminal_blocker_count: u32,
    pub justified_inapplicable_count: u32,
}

/// Complete compiled review, including its publishable input and read-only derived scope.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CompiledApplicabilityReview {
    pub input: ConformanceScopeInput,
    pub scope: ConformanceScope,
    pub audit: ApplicabilityAudit,
}

/// Expands exact groups, admits the complete scope, and renders a deterministic neutral audit.
pub fn compile_applicability_review(
    ledger: &ResolvedLedger,
    reviewed: &ReviewedConformanceScope,
    review: &ApplicabilityReviewInput,
) -> Result<CompiledApplicabilityReview, ConformanceDiagnosticSet> {
    let input = expand_applicability_review(ledger, reviewed, review)?;
    let scope = derive_conformance_scope(ledger, reviewed, input.clone())?;
    let audit = audit_applicability_review(ledger, reviewed, review, &scope)?;
    Ok(CompiledApplicabilityReview {
        input,
        scope,
        audit,
    })
}

/// Expands every explicit group identity to one locally anchored record.
pub fn expand_applicability_review(
    ledger: &ResolvedLedger,
    reviewed: &ReviewedConformanceScope,
    review: &ApplicabilityReviewInput,
) -> Result<ConformanceScopeInput, ConformanceDiagnosticSet> {
    validate_existing_conformance_scope(ledger, reviewed)?;
    let mut diagnostics = Vec::new();
    if review.format_version != reviewed.format_version
        || review.target_digest != reviewed.target_digest
        || review.existing_scope_digest != reviewed.existing_scope_digest
    {
        diagnostics.push(review_diagnostic(
            ConformanceDiagnosticCode::ConformanceScopeChanged,
            None,
            "applicability review identity is stale",
        ));
    }

    let mut group_ids = BTreeSet::new();
    let mut owners = BTreeMap::<CapabilityId, ApplicabilityGroupId>::new();
    for group in &review.groups {
        if !group_ids.insert(group.group_id.clone()) {
            diagnostics.push(review_diagnostic(
                ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
                None,
                "applicability review group is duplicated",
            ));
        }
        if group.capability_ids.is_empty() {
            diagnostics.push(review_diagnostic(
                ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
                None,
                "applicability review group is empty",
            ));
        }
        for capability_id in group.capability_ids.iter() {
            if owners
                .insert(capability_id.clone(), group.group_id.clone())
                .is_some()
            {
                diagnostics.push(review_diagnostic(
                    ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
                    Some(capability_id.clone()),
                    "applicability capability belongs to more than one group",
                ));
            }
        }
    }

    let expected = reviewed
        .existing_capability_ids
        .iter()
        .cloned()
        .collect::<BTreeSet<_>>();
    let observed = owners.keys().cloned().collect::<BTreeSet<_>>();
    for capability_id in expected.difference(&observed) {
        diagnostics.push(review_diagnostic(
            ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
            Some(capability_id.clone()),
            "applicability review omits capability",
        ));
    }
    for capability_id in observed.difference(&expected) {
        diagnostics.push(review_diagnostic(
            ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
            Some(capability_id.clone()),
            "applicability review contains capability outside scope",
        ));
    }
    if let Some(errors) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(errors);
    }

    let mut existing_records = Vec::with_capacity(observed.len());
    for group in &review.groups {
        let (assertions, cases) = expand_routes(group)?;
        for capability_id in group.capability_ids.iter() {
            let current = ledger
                .capabilities
                .get(capability_id)
                .expect("exact reviewed capability was validated against the ledger");
            let anchor = authority_anchor(current).ok_or_else(|| {
                one_review_diagnostic(
                    ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
                    Some(capability_id.clone()),
                    "applicability capability does not have one exact authority anchor",
                )
            })?;
            let assertion_ids = assertions.for_capability(capability_id)?;
            let case_ids = cases.for_capability(capability_id)?;
            let (disposition, decision_evidence) = expand_decision(group, &assertion_ids);
            existing_records.push(ApplicabilityRecord {
                capability_id: capability_id.clone(),
                authority_anchor: anchor,
                source_fingerprint: current.capability_fingerprint.clone(),
                disposition,
                assertion_ids,
                case_ids,
                decision_evidence,
                terminal_policy: group.terminal_policy.clone(),
            });
        }
    }
    existing_records.sort_unstable_by(|left, right| left.capability_id.cmp(&right.capability_id));

    Ok(ConformanceScopeInput {
        format_version: review.format_version,
        target_digest: review.target_digest.clone(),
        existing_scope_digest: review.existing_scope_digest.clone(),
        existing_records,
        policy_capabilities: reviewed_policy_capabilities(),
    })
}

fn expand_decision(
    group: &ApplicabilityReviewGroup,
    assertion_ids: &CanonicalSet<AssertionId>,
) -> (ApplicabilityDisposition, Option<ApplicabilityDecision>) {
    match &group.decision {
        ApplicabilityGroupDecision::SameMechanism => {
            (ApplicabilityDisposition::RustObservableSameMechanism, None)
        }
        ApplicabilityGroupDecision::IdiomaticEquivalence {
            observable_contract,
            rust_mechanism,
        } => (
            ApplicabilityDisposition::RustObservableIdiomatic,
            Some(ApplicabilityDecision::IdiomaticEquivalence {
                observable_contract: observable_contract.clone(),
                rust_mechanism: rust_mechanism.clone(),
            }),
        ),
        ApplicabilityGroupDecision::EngineOwnedNoRustObligation => (
            ApplicabilityDisposition::EngineOwnedNoRustObligation,
            Some(ApplicabilityDecision::EngineOwned {
                no_rust_input: true,
                no_rust_output: true,
                no_rust_lifecycle: true,
                no_rust_compatibility: true,
            }),
        ),
        ApplicabilityGroupDecision::ForeignSdkNoRustObligation { foreign_mechanism } => (
            ApplicabilityDisposition::ForeignSdkNoRustObligation,
            Some(ApplicabilityDecision::ForeignSdk {
                foreign_mechanism: foreign_mechanism.clone(),
                shared_assertion_ids: assertion_ids.clone(),
            }),
        ),
    }
}

#[derive(Clone)]
enum ExpandedRoutes<T> {
    None,
    PerCapability(T),
    Shared(CanonicalSet<T>),
}

impl<T> ExpandedRoutes<T>
where
    T: Clone + Ord + RouteId,
{
    fn for_capability(
        &self,
        capability_id: &CapabilityId,
    ) -> Result<CanonicalSet<T>, ConformanceDiagnosticSet> {
        match self {
            Self::None => Ok(CanonicalSet::default()),
            Self::Shared(ids) => Ok(ids.clone()),
            Self::PerCapability(namespace) => {
                let digest = Digest::sha256(capability_id.as_str());
                let suffix = digest
                    .as_str()
                    .strip_prefix("sha256:")
                    .expect("digest has canonical prefix");
                let value = format!("{}/{suffix}", namespace.as_route_str());
                T::from_route(value).map(|id| CanonicalSet::new([id]))
            }
        }
    }
}

trait RouteId {
    fn as_route_str(&self) -> &str;
    fn from_route(value: String) -> Result<Self, ConformanceDiagnosticSet>
    where
        Self: Sized;
}

impl RouteId for AssertionId {
    fn as_route_str(&self) -> &str {
        self.as_str()
    }

    fn from_route(value: String) -> Result<Self, ConformanceDiagnosticSet> {
        Self::new(value).map_err(|_| {
            one_review_diagnostic(
                ConformanceDiagnosticCode::ApplicabilityDecisionInvalid,
                None,
                "derived assertion identity exceeds the closed identifier contract",
            )
        })
    }
}

impl RouteId for SignoffCaseId {
    fn as_route_str(&self) -> &str {
        self.as_str()
    }

    fn from_route(value: String) -> Result<Self, ConformanceDiagnosticSet> {
        Self::new(value).map_err(|_| {
            one_review_diagnostic(
                ConformanceDiagnosticCode::ApplicabilityDecisionInvalid,
                None,
                "derived case identity exceeds the closed identifier contract",
            )
        })
    }
}

fn expand_routes(
    group: &ApplicabilityReviewGroup,
) -> Result<(ExpandedRoutes<AssertionId>, ExpandedRoutes<SignoffCaseId>), ConformanceDiagnosticSet>
{
    let routes = match &group.routing {
        ApplicabilityGroupRouting::None => (ExpandedRoutes::None, ExpandedRoutes::None),
        ApplicabilityGroupRouting::PerCapability {
            assertion_namespace,
            case_namespace,
        } => (
            ExpandedRoutes::PerCapability(assertion_namespace.clone()),
            case_namespace.as_ref().map_or(ExpandedRoutes::None, |id| {
                ExpandedRoutes::PerCapability(id.clone())
            }),
        ),
        ApplicabilityGroupRouting::Shared {
            assertion_ids,
            case_ids,
        } => (
            ExpandedRoutes::Shared(assertion_ids.clone()),
            ExpandedRoutes::Shared(case_ids.clone()),
        ),
    };
    Ok(routes)
}

fn audit_applicability_review(
    ledger: &ResolvedLedger,
    reviewed: &ReviewedConformanceScope,
    review: &ApplicabilityReviewInput,
    scope: &ConformanceScope,
) -> Result<ApplicabilityAudit, ConformanceDiagnosticSet> {
    let mut source_counts = BTreeMap::new();
    let mut disposition_counts = BTreeMap::new();
    let mut terminal_policy_counts = BTreeMap::new();
    let mut assertion_counts = BTreeMap::new();
    let mut current_blocker_count = 0_u32;
    let mut projected_terminal_blocker_count = 0_u32;
    for record in scope.existing_records().values() {
        *source_counts
            .entry(record.authority_anchor.path.clone())
            .or_insert(0) += 1;
        *disposition_counts
            .entry(record.disposition.clone())
            .or_insert(0) += 1;
        *terminal_policy_counts
            .entry(record.terminal_policy.clone())
            .or_insert(0) += 1;
        for assertion_id in record.assertion_ids.iter() {
            *assertion_counts.entry(assertion_id.clone()).or_insert(0) += 1;
        }
        let current = ledger
            .capabilities
            .get(&record.capability_id)
            .expect("admitted record exists in the current ledger");
        current_blocker_count += u32::from(is_blocking(&current.status));
        projected_terminal_blocker_count += u32::from(is_blocking(&record.terminal_policy));
    }
    assertion_counts.retain(|_, count| *count > 1);
    let justified_inapplicable_count = terminal_policy_counts
        .get(&Status::Inapplicable)
        .copied()
        .unwrap_or_default();
    let record_digest = canonical_digest(
        DigestDomain::ConformanceApplicabilityRecords,
        scope.existing_records(),
    )
    .map_err(|_| {
        one_review_diagnostic(
            ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
            None,
            "applicability records cannot be encoded canonically",
        )
    })?;
    let review_digest = canonical_digest(DigestDomain::ConformanceApplicabilityReview, review)
        .map_err(|_| {
            one_review_diagnostic(
                ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
                None,
                "applicability review cannot be encoded canonically",
            )
        })?;
    Ok(ApplicabilityAudit {
        format_version: reviewed.format_version,
        target_digest: reviewed.target_digest.clone(),
        existing_scope_digest: reviewed.existing_scope_digest.clone(),
        admitted_scope_digest: scope.digest().clone(),
        review_digest,
        record_digest,
        group_count: u32::try_from(review.groups.len()).expect("group count fits u32"),
        existing_record_count: u32::try_from(scope.existing_records().len())
            .expect("record count fits u32"),
        source_counts,
        disposition_counts,
        assertion_sharing: assertion_counts,
        terminal_policy_counts,
        current_blocker_count,
        projected_terminal_blocker_count,
        justified_inapplicable_count,
    })
}

fn review_diagnostic(
    code: ConformanceDiagnosticCode,
    capability_id: Option<CapabilityId>,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Applicability),
            capability_id,
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn one_review_diagnostic(
    code: ConformanceDiagnosticCode,
    capability_id: Option<CapabilityId>,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([review_diagnostic(code, capability_id, detail)])
        .expect("one applicability diagnostic is non-empty")
}
