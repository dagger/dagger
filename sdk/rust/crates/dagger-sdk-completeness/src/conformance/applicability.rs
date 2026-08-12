//! Exact Feature 8 inventory and fail-closed applicability artifact boundary.
//!
//! The authority-derived 1,081-row scope and the distinct Rust-policy scope are accounted for
//! separately. The initial checked artifacts intentionally contain no decisions: their empty
//! catalogs are valid canonical JSON but cannot be admitted as completed conformance evidence.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    AuthorityId, CanonicalSet, CapabilityId, CommitSha, Digest, EvidenceReference, FeatureId,
    RepositoryId, RepositoryRelativePath, ResolvedLedger, SourceItemKind, SourceLocator, Status,
    TargetDigest,
};

use super::{
    AssertionId, ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, SignoffCaseId,
};

/// Exact reviewed authority scope size at Feature 8 implementation start.
pub const EXISTING_CONFORMANCE_CAPABILITY_COUNT: usize = 1_081;
/// Exact missing integration-row count at Feature 8 implementation start.
pub const EXISTING_CONFORMANCE_MISSING_COUNT: usize = 1_072;
/// Exact partial definitive-client-row count at Feature 8 implementation start.
pub const EXISTING_CONFORMANCE_PARTIAL_COUNT: usize = 9;
/// Reviewed compact sorted-ID digest of the existing authority scope.
pub const EXISTING_CONFORMANCE_SCOPE_DIGEST: &str =
    "sha256:2969bd8fde19fc17d327cef637b9d848eca01040e88caffc09a4e9a4ad9bc5f9";

/// Exact 21-item Rust-policy scope, kept distinct from authority-derived capability accounting.
pub const CONFORMANCE_POLICY_CAPABILITY_IDS: [&str; 21] = [
    "policy/rust-policy/conformance-applicability-accounting",
    "policy/rust-policy/conformance-capability-scope",
    "policy/rust-policy/conformance-case-catalog",
    "policy/rust-policy/conformance-engine-free-checkpoint",
    "policy/rust-policy/platform-native-matrix",
    "policy/rust-policy/security-artifact-provenance",
    "policy/rust-policy/security-artifact-vulnerability-scan",
    "policy/rust-policy/security-expiring-exception",
    "policy/rust-policy/security-locked-supply-chain",
    "policy/rust-policy/security-secret-canary",
    "policy/rust-policy/signoff-artifact-import-reuse",
    "policy/rust-policy/signoff-atomic-verdict",
    "policy/rust-policy/signoff-case-retry-honesty",
    "policy/rust-policy/signoff-closure-evidence",
    "policy/rust-policy/signoff-duplicate-work-rejection",
    "policy/rust-policy/signoff-exact-target-artifact",
    "policy/rust-policy/signoff-host-preflight",
    "policy/rust-policy/signoff-isolated-case-fanout",
    "policy/rust-policy/signoff-phase-budget",
    "policy/rust-policy/signoff-single-engine",
    "policy/rust-policy/signoff-single-rust-baseline",
];

/// Checked canonical declaration of authority and Rust-policy inventory boundaries.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewedConformanceScope {
    pub format_version: ConformanceFormatVersion,
    pub target_digest: TargetDigest,
    pub existing_scope_digest: Digest,
    pub existing_status_counts: BTreeMap<Status, u32>,
    pub existing_capability_ids: CanonicalSet<CapabilityId>,
    pub existing_records: Vec<ReviewedScopeRecord>,
    pub policy_capability_ids: CanonicalSet<CapabilityId>,
}

/// Immutable authority/status snapshot for one row in the reviewed existing scope.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewedScopeRecord {
    pub capability_id: CapabilityId,
    pub authority_id: AuthorityId,
    pub source_anchors: CanonicalSet<EvidenceReference>,
    pub source_fingerprint: Digest,
    pub status: Status,
}

/// Exact deterministic scope drift categories shown before a replacement digest is considered.
#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConformanceScopeDelta {
    pub added: CanonicalSet<CapabilityId>,
    pub removed: CanonicalSet<CapabilityId>,
    pub moved: CanonicalSet<CapabilityId>,
    pub fingerprint_changed: CanonicalSet<CapabilityId>,
}

impl ConformanceScopeDelta {
    /// Returns true only when every reviewed row is byte-semantically unchanged.
    pub fn is_empty(&self) -> bool {
        self.added.is_empty()
            && self.removed.is_empty()
            && self.moved.is_empty()
            && self.fingerprint_changed.is_empty()
    }

    /// Renders stable category/identity lines without raw authority content or machine paths.
    pub fn render(&self) -> String {
        let mut lines = Vec::new();
        for (category, ids) in [
            ("added", &self.added),
            ("removed", &self.removed),
            ("moved", &self.moved),
            ("fingerprint-changed", &self.fingerprint_changed),
        ] {
            lines.extend(ids.iter().map(|id| format!("{category}:{}", id.as_str())));
        }
        lines.join("\n")
    }
}

/// Full immutable source coordinate for one applicability decision.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorityAnchor {
    pub repository: RepositoryId,
    pub revision: CommitSha,
    pub path: RepositoryRelativePath,
    pub locator: SourceLocator,
    pub source_item_kind: SourceItemKind,
}

/// Closed applicability choice; no generic catch-all disposition exists.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ApplicabilityDisposition {
    RustObservableSameMechanism,
    RustObservableIdiomatic,
    EngineOwnedNoRustObligation,
    ForeignSdkNoRustObligation,
}

/// Capability-local evidence required by equivalence or inapplicability decisions.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum ApplicabilityDecision {
    IdiomaticEquivalence {
        observable_contract: SourceLocator,
        rust_mechanism: SourceLocator,
    },
    EngineOwned {
        no_rust_input: bool,
        no_rust_output: bool,
        no_rust_lifecycle: bool,
        no_rust_compatibility: bool,
    },
    ForeignSdk {
        foreign_mechanism: SourceLocator,
        shared_assertion_ids: CanonicalSet<AssertionId>,
    },
}

/// One future reviewed decision for an existing authority capability.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApplicabilityRecord {
    pub capability_id: CapabilityId,
    pub authority_anchor: AuthorityAnchor,
    pub source_fingerprint: Digest,
    pub disposition: ApplicabilityDisposition,
    pub assertion_ids: CanonicalSet<AssertionId>,
    pub case_ids: CanonicalSet<SignoffCaseId>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub decision_evidence: Option<ApplicabilityDecision>,
    pub terminal_policy: Status,
}

/// One distinct reviewed Rust-policy capability and its stable requirement coordinate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PolicyCapability {
    pub capability_id: CapabilityId,
    pub fingerprint: Digest,
    pub requirement_coordinate: SourceLocator,
    pub owner_feature: FeatureId,
    pub blocking_status: Status,
}

/// Authored conformance scope input. Empty records are a scaffold, never successful evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConformanceScopeInput {
    pub format_version: ConformanceFormatVersion,
    pub target_digest: TargetDigest,
    pub existing_scope_digest: Digest,
    pub existing_records: Vec<ApplicabilityRecord>,
    pub policy_capabilities: Vec<PolicyCapability>,
}

/// Explicit non-decision emitted by scaffolding tools for one exact reviewed row.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ApplicabilityPlaceholder {
    pub capability_id: CapabilityId,
    pub authority_id: AuthorityId,
    pub source_anchors: CanonicalSet<EvidenceReference>,
    pub source_fingerprint: Digest,
    pub state: ApplicabilityPlaceholderState,
}

/// Closed marker which cannot be mistaken for an applicability disposition.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ApplicabilityPlaceholderState {
    ReviewRequired,
}

/// Produces one explicit placeholder for every exact reviewed identity.
pub fn scaffold_applicability_placeholders(
    reviewed: &ReviewedConformanceScope,
) -> Vec<ApplicabilityPlaceholder> {
    reviewed
        .existing_records
        .iter()
        .map(|record| ApplicabilityPlaceholder {
            capability_id: record.capability_id.clone(),
            authority_id: record.authority_id.clone(),
            source_anchors: record.source_anchors.clone(),
            source_fingerprint: record.source_fingerprint.clone(),
            state: ApplicabilityPlaceholderState::ReviewRequired,
        })
        .collect()
}

/// Fully admitted scope with canonical private indexes exposed read-only.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ConformanceScope {
    target_digest: TargetDigest,
    existing_records: BTreeMap<CapabilityId, ApplicabilityRecord>,
    policy_capabilities: BTreeMap<CapabilityId, PolicyCapability>,
    digest: Digest,
}

impl ConformanceScope {
    /// Returns the immutable target bound by this scope.
    pub fn target_digest(&self) -> &TargetDigest {
        &self.target_digest
    }

    /// Borrows admitted authority records by exact capability identity.
    pub fn existing_records(&self) -> &BTreeMap<CapabilityId, ApplicabilityRecord> {
        &self.existing_records
    }

    /// Borrows the distinct admitted policy capability map.
    pub fn policy_capabilities(&self) -> &BTreeMap<CapabilityId, PolicyCapability> {
        &self.policy_capabilities
    }

    /// Returns the domain-separated complete scope identity.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }
}

/// Canonical placeholder assertion artifact. An empty catalog is intentionally inadmissible.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConformanceAssertionScaffold {
    pub format_version: ConformanceFormatVersion,
    pub target_digest: TargetDigest,
    pub scope_digest: Digest,
    pub assertions: Vec<serde_json::Value>,
}

/// Canonical placeholder case artifact. An empty catalog is intentionally inadmissible.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConformanceCaseScaffold {
    pub format_version: ConformanceFormatVersion,
    pub target_digest: TargetDigest,
    pub scope_digest: Digest,
    pub cases: Vec<serde_json::Value>,
}

/// Validates that the current ledger still contains exactly the reviewed authority inventory.
pub fn existing_conformance_scope_delta(
    ledger: &ResolvedLedger,
    reviewed: &ReviewedConformanceScope,
) -> ConformanceScopeDelta {
    let observed = ledger
        .capabilities
        .values()
        .filter(|row| {
            row.owner_feature == Some(FeatureId::Feature8)
                && matches!(
                    row.authority_id.as_str(),
                    "go-client" | "go-integration-tests"
                )
        })
        .map(|row| {
            (
                row.capability_id.clone(),
                ReviewedScopeRecord {
                    capability_id: row.capability_id.clone(),
                    authority_id: row.authority_id.clone(),
                    source_anchors: row.source_anchors.clone(),
                    source_fingerprint: row.capability_fingerprint.clone(),
                    status: row.status.clone(),
                },
            )
        })
        .collect::<BTreeMap<_, _>>();
    let expected = reviewed
        .existing_records
        .iter()
        .cloned()
        .map(|record| (record.capability_id.clone(), record))
        .collect::<BTreeMap<_, _>>();
    let observed_ids = observed.keys().cloned().collect::<BTreeSet<_>>();
    let expected_ids = expected.keys().cloned().collect::<BTreeSet<_>>();
    let shared = observed_ids
        .intersection(&expected_ids)
        .cloned()
        .collect::<Vec<_>>();
    let moved = shared.iter().filter_map(|id| {
        let before = expected.get(id).expect("shared key exists");
        let after = observed.get(id).expect("shared key exists");
        (before.status != after.status
            || before.authority_id != after.authority_id
            || before.source_anchors != after.source_anchors)
            .then(|| id.clone())
    });
    let fingerprint_changed = shared
        .iter()
        .filter(|id| {
            expected
                .get(*id)
                .expect("shared key exists")
                .source_fingerprint
                != observed
                    .get(*id)
                    .expect("shared key exists")
                    .source_fingerprint
        })
        .cloned();
    ConformanceScopeDelta {
        added: CanonicalSet::new(observed_ids.difference(&expected_ids).cloned()),
        removed: CanonicalSet::new(expected_ids.difference(&observed_ids).cloned()),
        moved: CanonicalSet::new(moved),
        fingerprint_changed: CanonicalSet::new(fingerprint_changed),
    }
}

/// Validates that the current ledger still contains exactly the reviewed authority inventory.
pub fn validate_existing_conformance_scope(
    ledger: &ResolvedLedger,
    reviewed: &ReviewedConformanceScope,
) -> Result<(), ConformanceDiagnosticSet> {
    let rows = ledger
        .capabilities
        .values()
        .filter(|row| {
            row.owner_feature == Some(FeatureId::Feature8)
                && matches!(
                    row.authority_id.as_str(),
                    "go-client" | "go-integration-tests"
                )
        })
        .collect::<Vec<_>>();
    let ids = CanonicalSet::new(rows.iter().map(|row| row.capability_id.clone()));
    let compact = serde_json::to_vec(ids.as_slice()).expect("capability IDs always encode");
    let digest = Digest::sha256(compact);
    let missing = rows
        .iter()
        .filter(|row| row.status == Status::Missing)
        .count();
    let partial = rows
        .iter()
        .filter(|row| row.status == Status::Partial)
        .count();
    let exact_authorities = rows.iter().all(|row| match row.status {
        Status::Missing => row.authority_id.as_str() == "go-integration-tests",
        Status::Partial => row.authority_id.as_str() == "go-client",
        _ => false,
    });
    let policy = CanonicalSet::new(
        CONFORMANCE_POLICY_CAPABILITY_IDS
            .iter()
            .map(|id| CapabilityId::new(*id).expect("reviewed policy ID is valid")),
    );
    let valid = reviewed.format_version == ConformanceFormatVersion::V1
        && reviewed.existing_records.len() == EXISTING_CONFORMANCE_CAPABILITY_COUNT
        && rows.len() == EXISTING_CONFORMANCE_CAPABILITY_COUNT
        && missing == EXISTING_CONFORMANCE_MISSING_COUNT
        && partial == EXISTING_CONFORMANCE_PARTIAL_COUNT
        && exact_authorities
        && ids == reviewed.existing_capability_ids
        && digest.as_str() == EXISTING_CONFORMANCE_SCOPE_DIGEST
        && reviewed.existing_scope_digest == digest
        && reviewed.policy_capability_ids == policy
        && reviewed.existing_status_counts
            == BTreeMap::from([(Status::Missing, 1_072), (Status::Partial, 9)])
        && existing_conformance_scope_delta(ledger, reviewed).is_empty();
    if valid {
        Ok(())
    } else {
        Err(one_scope_diagnostic(
            ConformanceDiagnosticCode::ConformanceScopeChanged,
            "existing conformance scope count set status or digest changed",
        ))
    }
}

/// Compiles the complete reviewed scope. The checked initial scaffold fails until every row is
/// reviewed, preventing inventory existence from becoming implementation evidence.
pub fn derive_conformance_scope(
    ledger: &ResolvedLedger,
    reviewed: &ReviewedConformanceScope,
    input: ConformanceScopeInput,
) -> Result<ConformanceScope, ConformanceDiagnosticSet> {
    validate_existing_conformance_scope(ledger, reviewed)?;
    let mut diagnostics = Vec::new();
    if input.format_version != reviewed.format_version
        || input.target_digest != reviewed.target_digest
        || input.existing_scope_digest != reviewed.existing_scope_digest
    {
        diagnostics.push(scope_diagnostic(
            ConformanceDiagnosticCode::ConformanceScopeChanged,
            "conformance scope input identity is stale",
        ));
    }
    let existing = input
        .existing_records
        .into_iter()
        .map(|record| (record.capability_id.clone(), record))
        .collect::<BTreeMap<_, _>>();
    if existing.len() != EXISTING_CONFORMANCE_CAPABILITY_COUNT
        || existing.keys().cloned().collect::<BTreeSet<_>>()
            != reviewed.existing_capability_ids.iter().cloned().collect()
    {
        diagnostics.push(scope_diagnostic(
            ConformanceDiagnosticCode::ApplicabilityRecordInvalid,
            "applicability records are incomplete duplicated or out of scope",
        ));
    }
    let policies = input
        .policy_capabilities
        .into_iter()
        .map(|policy| (policy.capability_id.clone(), policy))
        .collect::<BTreeMap<_, _>>();
    if policies.len() != CONFORMANCE_POLICY_CAPABILITY_IDS.len()
        || policies.keys().cloned().collect::<BTreeSet<_>>()
            != reviewed.policy_capability_ids.iter().cloned().collect()
    {
        diagnostics.push(scope_diagnostic(
            ConformanceDiagnosticCode::ConformancePolicyScopeChanged,
            "Rust policy capability inventory is incomplete duplicated or changed",
        ));
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }
    let digest = canonical_digest(
        DigestDomain::ConformanceScope,
        &(&input.target_digest, &existing, &policies),
    )
    .map_err(|_| {
        one_scope_diagnostic(
            ConformanceDiagnosticCode::ConformanceScopeChanged,
            "conformance scope cannot be encoded canonically",
        )
    })?;
    Ok(ConformanceScope {
        target_digest: input.target_digest,
        existing_records: existing,
        policy_capabilities: policies,
        digest,
    })
}

/// Builds the exact policy inventory with deterministic fingerprints and blocking ownership.
pub fn reviewed_policy_capabilities() -> Vec<PolicyCapability> {
    CONFORMANCE_POLICY_CAPABILITY_IDS
        .iter()
        .map(|id| {
            let capability_id = CapabilityId::new(*id).expect("reviewed policy ID is valid");
            let suffix = id.rsplit('/').next().expect("policy ID has a suffix");
            let requirement_coordinate = policy_requirement_coordinate(suffix);
            let fingerprint = canonical_digest(
                DigestDomain::ConformancePolicy,
                &(&capability_id, &requirement_coordinate),
            )
            .expect("reviewed policy capability encodes canonically");
            PolicyCapability {
                capability_id,
                fingerprint,
                requirement_coordinate,
                owner_feature: FeatureId::Feature8,
                blocking_status: Status::Missing,
            }
        })
        .collect()
}

fn policy_requirement_coordinate(suffix: &str) -> SourceLocator {
    let coordinate = match suffix {
        "conformance-capability-scope" => "requirement-1.1-1.4",
        "conformance-applicability-accounting" => "requirement-1.5-1.18",
        "conformance-case-catalog" => "requirement-3.1-3.24",
        "conformance-engine-free-checkpoint" => "requirement-11.1-11.20",
        "signoff-host-preflight" => "requirement-2.1-2.20",
        "signoff-exact-target-artifact" => "requirement-5.1-5.20",
        "signoff-artifact-import-reuse" => "requirement-5.7-5.16",
        "signoff-closure-evidence" => "requirement-4.1-4.19",
        "signoff-single-engine" => "requirement-6.1-6.6",
        "signoff-single-rust-baseline" => "requirement-6.7-6.9",
        "signoff-isolated-case-fanout" => "requirement-6.10-6.22",
        "signoff-case-retry-honesty" => "requirement-6.17-6.22",
        "signoff-atomic-verdict" => "requirement-12.1-12.35",
        "signoff-duplicate-work-rejection" => "requirement-12.13-12.21",
        "signoff-phase-budget" => "requirement-12.22-12.26",
        "platform-native-matrix" => "requirement-8.1-8.21",
        "security-locked-supply-chain" => "requirement-9.1-9.7",
        "security-artifact-provenance" => "requirement-9.8-9.17",
        "security-artifact-vulnerability-scan" => "requirement-9.18-9.25",
        "security-secret-canary" => "requirement-10.1-10.17",
        "security-expiring-exception" => "requirement-9.21-9.25",
        _ => unreachable!("reviewed policy suffix has a requirement coordinate"),
    };
    SourceLocator::new(coordinate).expect("reviewed requirement coordinate is valid")
}

fn scope_diagnostic(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Scope),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn one_scope_diagnostic(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([scope_diagnostic(code, detail)])
        .expect("one diagnostic is non-empty")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn policy_inventory_is_exact_distinct_and_blocking() {
        let policies = reviewed_policy_capabilities();
        assert_eq!(policies.len(), 21);
        assert_eq!(
            policies
                .iter()
                .map(|item| &item.capability_id)
                .collect::<BTreeSet<_>>()
                .len(),
            21
        );
        assert!(policies.iter().all(|item| {
            item.owner_feature == FeatureId::Feature8 && item.blocking_status == Status::Missing
        }));
    }
}
