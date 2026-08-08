//! Exact core-codegen ownership correction and projection-catalog boundary.
//!
//! Core code generation starts from a reviewed pre-transition ledger rather than a
//! hand-copied 3,261-line scope fence. The compact contract pins that ledger, the three
//! correction groups, and the retained projection independently; any source or
//! fingerprint drift therefore fails before ownership can be rewritten.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::feature_scope::{FeatureContractPolicy, FeatureScopePolicy, ReviewedPolicyClause};
use crate::model::{
    CanonicalInventory, CanonicalSet, CapabilityId, CapabilityRecord, Digest, FeatureId,
    RepositoryId, RepositoryRelativePath, ResolvedLedger, Status,
};
use crate::traceability::FeatureScopeDeclaration;

/// Repository-relative source containing the approved core-codegen requirements.
pub const REQUIREMENTS_PATH: &str = ".kiro/specs/rust-sdk-core-codegen/requirements.md";

/// Golden description of the core-codegen rows before ownership correction.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BaselineLedgerContract {
    /// Number of existing rows routed to core code generation before correction.
    pub existing_count: usize,
    /// Digest of the complete ordered capability-record map.
    pub rows_digest: Digest,
    /// Digest of ordered capability ID and status pairs.
    pub status_digest: Digest,
    /// Digest of ordered capability ID and fingerprint pairs.
    pub fingerprint_digest: Digest,
}

/// Golden description of the existing rows retained by core code generation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RetainedScopeContract {
    /// Exact retained row count.
    pub expected_count: usize,
    /// Digest of the ordered retained capability IDs.
    pub capability_ids_digest: Digest,
    /// Digest of ordered retained capability ID and status pairs.
    pub status_digest: Digest,
    /// Digest of ordered retained capability ID and fingerprint pairs.
    pub fingerprint_digest: Digest,
}

/// One exact owner-routing correction selected by IDs or reviewed source paths.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OwnershipCorrectionContract {
    /// Feature that owns the rows after correction.
    pub destination: FeatureId,
    /// Number of rows selected by this correction.
    pub expected_count: usize,
    /// Digest of the exact ordered selected capability IDs.
    pub capability_ids_digest: Digest,
    /// Explicit IDs used for the small Go-client correction.
    pub capability_ids: Vec<CapabilityId>,
    /// Exact source paths used for coherent Go-codegen groups.
    pub source_paths: Vec<RepositoryRelativePath>,
}

/// Compact, machine-readable contract for the complete ownership transition.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CoreCodegenScopeContract {
    /// Contract schema version.
    pub format_version: crate::model::SemverVersion,
    /// Immutable pre-transition ledger projection.
    pub baseline: BaselineLedgerContract,
    /// Immutable retained core-codegen projection.
    pub retained: RetainedScopeContract,
    /// Exact correction groups in reviewed destination order.
    pub corrections: Vec<OwnershipCorrectionContract>,
    /// New Rust-policy capability IDs in canonical order.
    pub policy_capability_ids: Vec<CapabilityId>,
}

/// Validated result of applying the reviewed owner-only correction.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CoreCodegenScopeTransition {
    /// Ledger with only the approved ownership changes applied.
    pub ledger: ResolvedLedger,
    /// Exact retained and new-policy core-codegen declaration.
    pub declaration: FeatureScopeDeclaration,
    /// Routing policy paired with the derived declaration.
    pub policy: FeatureScopePolicy,
    /// Corrected IDs grouped by their destination feature.
    pub corrected_capability_ids: BTreeMap<FeatureId, CanonicalSet<CapabilityId>>,
}

/// Constructs the policy-extraction contract used before the existing scope is derived.
pub fn core_codegen_policy_contract(contract: &CoreCodegenScopeContract) -> FeatureContractPolicy {
    let policy_capability_ids = CanonicalSet::new(contract.policy_capability_ids.clone());
    FeatureContractPolicy {
        requirements_path: REQUIREMENTS_PATH,
        scope: FeatureScopePolicy {
            feature: FeatureId::Feature4,
            existing_scope_heading: "Feature 4 exact retained scope is derived from core-codegen-scope.json",
            policy_scope_heading: "### Omitted Rust Policy Capabilities",
            existing_capability_ids: CanonicalSet::default(),
            existing_scope_digest: contract.retained.capability_ids_digest.clone(),
            expected_prior_blocking_owners: policy_capability_ids
                .iter()
                .cloned()
                .map(|capability_id| (capability_id, FeatureId::Feature4))
                .collect(),
            policy_capability_ids,
            evidence_repository: RepositoryId::new("github.com/dagger/dagger")
                .expect("static evidence repository must be valid"),
        },
        policy_clauses: CORE_CODEGEN_POLICIES,
    }
}

/// Applies and validates the exact core-codegen ownership transition.
pub fn apply_core_codegen_scope_correction(
    inventory: &CanonicalInventory,
    before: &ResolvedLedger,
    contract: &CoreCodegenScopeContract,
) -> Validation<CoreCodegenScopeTransition> {
    let mut diagnostics = DiagnosticCollector::default();
    validate_contract_shape(contract, &mut diagnostics);
    diagnostics.finish(())?;
    let mut diagnostics = DiagnosticCollector::default();

    let baseline = before
        .capabilities
        .iter()
        .filter(|(_, row)| {
            row.owner_feature == Some(FeatureId::Feature4)
                && row.authority_id.as_str() != "rust-policy"
        })
        .collect::<BTreeMap<_, _>>();
    if baseline.len() != contract.baseline.existing_count {
        diagnostics.push(scope_diagnostic(
            "baseline rows",
            format!(
                "expected {} rows, observed {}",
                contract.baseline.existing_count,
                baseline.len()
            ),
        ));
    }
    validate_policy_rows(
        inventory,
        before,
        &CanonicalSet::new(contract.policy_capability_ids.clone()),
        &mut diagnostics,
    );
    diagnostics.finish(())?;
    let mut diagnostics = DiagnosticCollector::default();

    let mut corrected_by_feature = BTreeMap::new();
    let mut all_corrected = BTreeSet::new();
    for correction in &contract.corrections {
        let selected = select_correction(&baseline, correction, &mut diagnostics);
        for capability_id in &selected {
            if !all_corrected.insert(capability_id.clone()) {
                diagnostics.push(scope_diagnostic(
                    capability_id,
                    "capability appears in more than one owner correction",
                ));
            }
        }
        corrected_by_feature.insert(correction.destination.clone(), CanonicalSet::new(selected));
    }
    diagnostics.finish(())?;
    let mut diagnostics = DiagnosticCollector::default();

    let retained = baseline
        .iter()
        .filter(|(capability_id, _)| !all_corrected.contains(*capability_id))
        .map(|(capability_id, row)| (*capability_id, *row))
        .collect::<BTreeMap<_, _>>();
    validate_baseline_projections(&baseline, &contract.baseline, &mut diagnostics);
    validate_retained(&retained, &contract.retained, &mut diagnostics);
    diagnostics.finish(())?;
    let mut diagnostics = DiagnosticCollector::default();

    let rows_digest = direct_digest(&baseline);
    if rows_digest != contract.baseline.rows_digest {
        diagnostics.push(scope_diagnostic(
            "baseline rows",
            format!(
                "row projection changed: expected {}, observed {rows_digest}",
                contract.baseline.rows_digest
            ),
        ));
    }

    let policy_ids = CanonicalSet::new(contract.policy_capability_ids.clone());
    diagnostics.finish(())?;

    let mut ledger = before.clone();
    for (destination, capability_ids) in &corrected_by_feature {
        for capability_id in capability_ids.iter() {
            let row = ledger
                .capabilities
                .get_mut(capability_id)
                .expect("validated correction rows must remain present");
            row.owner_feature = Some(destination.clone());
        }
    }

    let mut preservation_diagnostics = DiagnosticCollector::default();
    for (capability_id, before_row) in &before.capabilities {
        let after_row = ledger
            .capabilities
            .get(capability_id)
            .expect("owner-only correction cannot remove rows");
        if before_row.status != after_row.status {
            preservation_diagnostics.push(scope_diagnostic(
                capability_id,
                "ownership correction changed capability status",
            ));
        }
        if !all_corrected.contains(capability_id) && before_row != after_row {
            preservation_diagnostics.push(scope_diagnostic(
                capability_id,
                "capability outside the correction set was not byte-equivalent",
            ));
        }
        if all_corrected.contains(capability_id) {
            let mut expected = before_row.clone();
            expected.owner_feature = after_row.owner_feature.clone();
            if &expected != after_row {
                preservation_diagnostics.push(scope_diagnostic(
                    capability_id,
                    "correction changed a field other than owner_feature",
                ));
            }
        }
    }

    let retained_ids = CanonicalSet::new(
        retained
            .keys()
            .map(|capability_id| (**capability_id).clone()),
    );
    let scope_ids = retained_ids
        .iter()
        .chain(policy_ids.iter())
        .cloned()
        .collect::<Vec<_>>();
    let expected_prior_blocking_owners = scope_ids
        .into_iter()
        .map(|capability_id| (capability_id, FeatureId::Feature4))
        .collect();
    let policy = FeatureScopePolicy {
        feature: FeatureId::Feature4,
        existing_scope_heading: "core-codegen-scope.json retained projection",
        policy_scope_heading: "### Omitted Rust Policy Capabilities",
        existing_capability_ids: retained_ids.clone(),
        existing_scope_digest: contract.retained.capability_ids_digest.clone(),
        policy_capability_ids: policy_ids.clone(),
        expected_prior_blocking_owners,
        evidence_repository: RepositoryId::new("github.com/dagger/dagger")
            .expect("static evidence repository must be valid"),
    };
    let declaration = FeatureScopeDeclaration {
        feature: FeatureId::Feature4,
        existing_capability_ids: retained_ids,
        existing_scope_digest: contract.retained.capability_ids_digest.clone(),
        policy_capability_ids: policy_ids,
    };

    preservation_diagnostics.finish(CoreCodegenScopeTransition {
        ledger,
        declaration,
        policy,
        corrected_capability_ids: corrected_by_feature,
    })
}

fn validate_contract_shape(
    contract: &CoreCodegenScopeContract,
    diagnostics: &mut DiagnosticCollector,
) {
    let expected_destinations = [
        FeatureId::Feature3,
        FeatureId::Feature5,
        FeatureId::Feature6,
    ];
    let destinations = contract
        .corrections
        .iter()
        .map(|correction| correction.destination.clone())
        .collect::<Vec<_>>();
    if destinations != expected_destinations {
        diagnostics.push(scope_diagnostic(
            "corrections",
            "owner corrections must appear exactly once in Feature 3, 5, 6 order",
        ));
    }
    if !is_strictly_sorted(&contract.policy_capability_ids) {
        diagnostics.push(scope_diagnostic(
            "policy_capability_ids",
            "policy capability IDs must be canonical and duplicate-free",
        ));
    }
    let expected_policies = CORE_CODEGEN_POLICIES
        .iter()
        .map(|clause| {
            CapabilityId::new(format!("policy/rust-policy/{}", clause.clause_id))
                .expect("static policy capability ID must be valid")
        })
        .collect::<Vec<_>>();
    if contract.policy_capability_ids != expected_policies {
        diagnostics.push(scope_diagnostic(
            "policy_capability_ids",
            "policy capability IDs differ from the 16 reviewed clauses",
        ));
    }
    for correction in &contract.corrections {
        if !is_strictly_sorted(&correction.capability_ids)
            || !is_strictly_sorted(&correction.source_paths)
        {
            diagnostics.push(scope_diagnostic(
                format!("{:?}", correction.destination),
                "correction selectors must be canonical and duplicate-free",
            ));
        }
        if correction.capability_ids.is_empty() == correction.source_paths.is_empty() {
            diagnostics.push(scope_diagnostic(
                format!("{:?}", correction.destination),
                "each correction must use exactly one selector form",
            ));
        }
    }
}

fn validate_baseline_projections(
    baseline: &BTreeMap<&CapabilityId, &CapabilityRecord>,
    contract: &BaselineLedgerContract,
    diagnostics: &mut DiagnosticCollector,
) {
    check_projection_digest(
        "baseline statuses",
        status_projection(baseline),
        &contract.status_digest,
        diagnostics,
    );
    check_projection_digest(
        "baseline fingerprints",
        fingerprint_projection(baseline),
        &contract.fingerprint_digest,
        diagnostics,
    );
}

fn validate_retained(
    retained: &BTreeMap<&CapabilityId, &CapabilityRecord>,
    contract: &RetainedScopeContract,
    diagnostics: &mut DiagnosticCollector,
) {
    let ids = retained
        .keys()
        .map(|capability_id| (**capability_id).clone())
        .collect::<Vec<_>>();
    check_count_digest(
        "retained scope",
        ids.len(),
        contract.expected_count,
        direct_digest(&ids),
        &contract.capability_ids_digest,
        diagnostics,
    );
    check_projection_digest(
        "retained statuses",
        status_projection(retained),
        &contract.status_digest,
        diagnostics,
    );
    check_projection_digest(
        "retained fingerprints",
        fingerprint_projection(retained),
        &contract.fingerprint_digest,
        diagnostics,
    );
}

fn select_correction(
    baseline: &BTreeMap<&CapabilityId, &CapabilityRecord>,
    correction: &OwnershipCorrectionContract,
    diagnostics: &mut DiagnosticCollector,
) -> Vec<CapabilityId> {
    let selected = if !correction.capability_ids.is_empty() {
        correction.capability_ids.clone()
    } else {
        let paths = correction.source_paths.iter().collect::<BTreeSet<_>>();
        baseline
            .iter()
            .filter(|(_, row)| {
                row.source_anchors
                    .iter()
                    .any(|anchor| paths.contains(&anchor.path))
            })
            .map(|(capability_id, _)| (**capability_id).clone())
            .collect()
    };
    for capability_id in &selected {
        if !baseline.keys().any(|candidate| *candidate == capability_id) {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "correction selected a capability outside the reviewed Feature 4 baseline",
            ));
        }
    }
    check_count_digest(
        format!("{:?} correction", correction.destination),
        selected.len(),
        correction.expected_count,
        direct_digest(&selected),
        &correction.capability_ids_digest,
        diagnostics,
    );
    selected
}

fn validate_policy_rows(
    inventory: &CanonicalInventory,
    ledger: &ResolvedLedger,
    policy_ids: &CanonicalSet<CapabilityId>,
    diagnostics: &mut DiagnosticCollector,
) {
    for capability_id in policy_ids.iter() {
        if !inventory.capabilities.contains_key(capability_id) {
            diagnostics.push(scope_diagnostic(
                capability_id,
                "reviewed core-codegen policy is absent from the inventory",
            ));
        }
        match ledger.capabilities.get(capability_id) {
            Some(row)
                if row.owner_feature == Some(FeatureId::Feature4)
                    && row.status == Status::Missing => {}
            Some(_) => diagnostics.push(scope_diagnostic(
                capability_id,
                "new core-codegen policy must begin Missing and owned by Feature 4",
            )),
            None => diagnostics.push(scope_diagnostic(
                capability_id,
                "reviewed core-codegen policy is absent from the ledger",
            )),
        }
    }
}

fn status_projection(
    rows: &BTreeMap<&CapabilityId, &CapabilityRecord>,
) -> Vec<(CapabilityId, Status)> {
    rows.iter()
        .map(|(capability_id, row)| ((**capability_id).clone(), row.status.clone()))
        .collect()
}

fn fingerprint_projection(
    rows: &BTreeMap<&CapabilityId, &CapabilityRecord>,
) -> Vec<(CapabilityId, Digest)> {
    rows.iter()
        .map(|(capability_id, row)| {
            (
                (**capability_id).clone(),
                row.capability_fingerprint.clone(),
            )
        })
        .collect()
}

fn check_projection_digest<T: Serialize>(
    subject: &str,
    projection: T,
    expected: &Digest,
    diagnostics: &mut DiagnosticCollector,
) {
    let actual = direct_digest(&projection);
    if &actual != expected {
        diagnostics.push(scope_diagnostic(
            subject,
            format!("projection digest changed: expected {expected}, observed {actual}"),
        ));
    }
}

fn check_count_digest(
    subject: impl ToString,
    actual_count: usize,
    expected_count: usize,
    actual_digest: Digest,
    expected_digest: &Digest,
    diagnostics: &mut DiagnosticCollector,
) {
    if actual_count != expected_count || &actual_digest != expected_digest {
        diagnostics.push(scope_diagnostic(
            subject,
            format!(
                "expected {expected_count} rows and {expected_digest}, observed {actual_count} rows and {actual_digest}"
            ),
        ));
    }
}

fn direct_digest(value: &impl Serialize) -> Digest {
    serde_json::to_value(value)
        .and_then(|value| serde_json::to_vec(&value))
        .map(Digest::sha256)
        .unwrap_or_else(|_| Digest::sha256([]))
}

fn is_strictly_sorted<T: Ord>(values: &[T]) -> bool {
    values.windows(2).all(|pair| pair[0] < pair[1])
}

fn scope_diagnostic(subject: impl ToString, detail: impl Into<String>) -> ContractDiagnostic {
    ContractDiagnostic::new(
        DiagnosticCode::LedgerDrift,
        subject.to_string(),
        None,
        detail,
    )
}

const CORE_CODEGEN_POLICIES: &[ReviewedPolicyClause] = &[
    clause(
        "core-codegen-atomic-publication",
        "WHEN update mode succeeds, THE generation command SHALL publish each changed\n   Generated_Artifact atomically.",
        "explicit-ownership",
    ),
    clause(
        "core-codegen-authority-containment",
        "WHEN generation starts, THE Core_Generator SHALL verify the Canonical_Schema digest\n   against the Exact_Target authority registry.",
        "locked-resolution",
    ),
    clause(
        "core-codegen-collision-detection",
        "IF two distinct source names collide after normalization, THEN THE Core_Generator\n   SHALL return `RUST_NAME_COLLISION` with both schema coordinates.",
        "idiomatic-rust",
    ),
    clause(
        "core-codegen-default-omission",
        "WHEN a defaulted argument is omitted, THE generated binding SHALL avoid\n    materializing the schema default on the client.",
        "idiomatic-rust",
    ),
    clause(
        "core-codegen-directive-accounting",
        "WHILE a target directive has no active Core_Schema application, THE\n    Generated_Binding_Manifest SHALL record its validated target-inactive policy.",
        "explicit-ownership",
    ),
    clause(
        "core-codegen-documentation",
        "THE Core_Generator SHALL document every public generated type, trait, method,\n   options value, options field, scalar, enum, and enum variant.",
        "why-comments",
    ),
    clause(
        "core-codegen-exhaustive-manifest",
        "THE Generated_Binding_Manifest SHALL contain exactly one Binding_Record for every\n   Feature 4-owned Active_Capability.",
        "explicit-ownership",
    ),
    clause(
        "core-codegen-fallible-input",
        "WHEN caller-controlled schema input is malformed, THE Core_Generator SHALL return\n    an error without panic, `unwrap`, or invariant-free `expect` termination.",
        "panic-free-library",
    ),
    clause(
        "core-codegen-identifier-roundtrip",
        "WHEN a Rust-safe representation differs from the source name, THE generated binding\n   SHALL retain the exact Wire_Name for serialization or selection.",
        "idiomatic-rust",
    ),
    clause(
        "core-codegen-input-order-invariance",
        "IF validated input is semantically equivalent but differently ordered, THEN THE\n    Core_Generator SHALL construct the same canonical schema model.",
        "locked-resolution",
    ),
    clause(
        "core-codegen-list-object-reentry",
        "IF list re-entry targets a type without the required ID surface, THEN THE\n    Core_Generator SHALL return `LIST_REENTRY_TYPE_INVALID`.",
        "typed-public-errors",
    ),
    clause(
        "core-codegen-no-handwritten-fixes",
        "WHEN source generation changes semantics, THE semantic change SHALL originate in\n    generator logic or templates rather than compiler fix-up output.",
        "locked-resolution",
    ),
    clause(
        "core-codegen-nullability",
        "WHEN `NON_NULL` and `LIST` wrappers are nested, THE Core_Generator SHALL preserve\n    their order recursively in the Rust type.",
        "idiomatic-rust",
    ),
    clause(
        "core-codegen-scalar-wire-types",
        "THE Core_Generator SHALL map GraphQL `Int` to platform-independent Rust `i64`.",
        "idiomatic-rust",
    ),
    clause(
        "core-codegen-target-drift",
        "WHEN the Exact_Target changes, THE completeness validator SHALL reject every\n    unreconciled Feature 4 capability addition, removal, fingerprint change, or owner\n    change.",
        "locked-resolution",
    ),
    clause(
        "core-codegen-toolchain-compatibility",
        "WHEN source formatting is required, THE generation command SHALL use the formatter\n    from the pinned Rust toolchain.",
        "cargo-deny",
    ),
];

const fn clause(
    clause_id: &'static str,
    exact_text: &'static str,
    guidance_id: &'static str,
) -> ReviewedPolicyClause {
    ReviewedPolicyClause {
        clause_id,
        exact_text,
        guidance_id,
    }
}
