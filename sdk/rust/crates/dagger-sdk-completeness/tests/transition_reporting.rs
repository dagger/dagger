//! Executable properties for transitions, compatibility, reports, gates, and staging rejection.
//!
//! Each property compares the implementation with a deliberately smaller reference model. The
//! filesystem property snapshots the complete active fixture tree, rather than checking only the
//! path an invalid command was expected to touch.

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::strategy::ValueTree;
use proptest::test_runner::TestRunner;
use proptest::test_runner::{Config, FileFailurePersistence};
use serde::Serialize;

#[allow(dead_code, unused_imports)]
mod support;

use support::equivalent_contract_cases_strategy;

const CASES: u32 = 256;

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/transition-reporting.txt"
        )))),
        ..Config::default()
    }
}

#[derive(Clone)]
struct OwnedSnapshot {
    target: TargetDescriptor,
    authorities: AuthorityRegistry,
    inventory: CanonicalInventory,
    ledger: ResolvedLedger,
    evidence: EvidenceRegistry,
    harness: HarnessMappings,
}

impl OwnedSnapshot {
    fn as_snapshot(&self) -> ContractSnapshot<'_> {
        ContractSnapshot {
            target: &self.target,
            authorities: &self.authorities,
            inventory: &self.inventory,
            ledger: &self.ledger,
            evidence: &self.evidence,
            harness_mappings: &self.harness,
        }
    }
}

fn target_digest(target: &TargetDescriptor) -> TargetDigest {
    TargetDigest::new(canonical_digest(DigestDomain::Target, target).unwrap())
}

fn transition_pair(case: &support::contract_case::ContractCase) -> (OwnedSnapshot, OwnedSnapshot) {
    let mut from_target = case.target.clone();
    from_target.previous_target = None;
    let from_digest = target_digest(&from_target);
    let mut to_target = from_target.clone();
    to_target.schema_digest = Digest::sha256("successor-schema");
    to_target.previous_target = Some(from_digest);

    let mut from_inventory = case.inventory.clone();
    let mut from_ledger = case.ledger.clone();
    let base_id = case.capability_record.capability_id.clone();
    from_inventory
        .capabilities
        .get_mut(&base_id)
        .unwrap()
        .stability = Stability::NotApplicable;
    from_ledger
        .capabilities
        .get_mut(&base_id)
        .unwrap()
        .stability = Stability::NotApplicable;

    let removable_id = CapabilityId::new("behavior/transition/removable").unwrap();
    let mut removable_definition = from_inventory.capabilities[&base_id].clone();
    removable_definition.capability_id = removable_id.clone();
    removable_definition.capability_fingerprint = Digest::sha256("removable-capability");
    let mut removable_record = from_ledger.capabilities[&base_id].clone();
    removable_record.capability_id = removable_id.clone();
    removable_record.capability_fingerprint = removable_definition.capability_fingerprint.clone();
    from_inventory
        .capabilities
        .insert(removable_id.clone(), removable_definition);
    from_ledger
        .capabilities
        .insert(removable_id, removable_record);

    let mut to_inventory = from_inventory.clone();
    let mut to_ledger = from_ledger.clone();
    let mut to_evidence = case.evidence_registry.clone();
    let to_digest = target_digest(&to_target);
    rebind_record_evidence(&mut to_evidence, &to_ledger, &to_digest);

    (
        OwnedSnapshot {
            target: from_target,
            authorities: case.authority_registry.clone(),
            inventory: from_inventory,
            ledger: from_ledger,
            evidence: case.evidence_registry.clone(),
            harness: case.harness_mappings.clone(),
        },
        OwnedSnapshot {
            target: to_target,
            authorities: case.authority_registry.clone(),
            inventory: std::mem::take(&mut to_inventory),
            ledger: std::mem::take(&mut to_ledger),
            evidence: to_evidence,
            harness: case.harness_mappings.clone(),
        },
    )
}

fn rebind_record_evidence(
    evidence: &mut EvidenceRegistry,
    ledger: &ResolvedLedger,
    target: &TargetDigest,
) {
    for evidence_id in ledger.capabilities.values().flat_map(|record| {
        record
            .implementation_evidence
            .iter()
            .chain(record.verification_evidence.iter())
    }) {
        if let Some(reference) = evidence.evidence.get_mut(evidence_id) {
            reference.execution_target = Some(target.clone());
        }
    }
}

// Invariant: the semantic differ equals a simple identity/fingerprint set model and preserves rows.
// Feature: rust-sdk-completeness-contract, Property 13: semantic drift and target-transition diff
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_13_semantic_drift_and_target_transition_diff(
        cases in equivalent_contract_cases_strategy(),
        add in any::<bool>(),
        remove in any::<bool>(),
        change in any::<bool>(),
        authority_change in any::<bool>(),
        harness_change in any::<bool>(),
    ) {
        let (from, mut to) = transition_pair(&cases.forward);
        let base_id = cases.forward.capability_record.capability_id.clone();
        let removable_id = CapabilityId::new("behavior/transition/removable").unwrap();
        let added_id = CapabilityId::new("behavior/transition/added").unwrap();

        if add {
            let mut definition = to.inventory.capabilities[&base_id].clone();
            definition.capability_id = added_id.clone();
            definition.capability_fingerprint = Digest::sha256("added-capability");
            let mut record = to.ledger.capabilities[&base_id].clone();
            record.capability_id = added_id.clone();
            record.capability_fingerprint = definition.capability_fingerprint.clone();
            to.inventory.capabilities.insert(added_id.clone(), definition);
            to.ledger.capabilities.insert(added_id.clone(), record);
        }
        if remove {
            to.inventory.capabilities.remove(&removable_id);
            to.ledger.capabilities.remove(&removable_id);
        }
        if change {
            let fingerprint = Digest::sha256("changed-capability");
            to.inventory.capabilities.get_mut(&base_id).unwrap().capability_fingerprint =
                fingerprint.clone();
            to.ledger.capabilities.get_mut(&base_id).unwrap().capability_fingerprint = fingerprint;
        }
        if authority_change {
            let source = to.authorities.authorities.values_mut().next().unwrap();
            source.source_digest = Digest::sha256("changed-authority-source");
        }
        if harness_change {
            let mapping = to.harness.checks.values_mut().next().unwrap();
            mapping.source_fingerprint = Digest::sha256("changed-harness-check");
        }

        let transition = diff_targets(
            from.as_snapshot(),
            to.as_snapshot(),
            &CanonicalSet::default(),
        ).unwrap();

        prop_assert_eq!(transition.added_capabilities.contains(&added_id), add);
        prop_assert_eq!(
            transition
                .removed_capabilities
                .iter()
                .any(|record| record.capability.capability_id == removable_id),
            remove
        );
        prop_assert_eq!(
            transition
                .changed_capabilities
                .iter()
                .any(|entry| entry.capability_id == base_id),
            change
        );
        prop_assert_eq!(!transition.authority_changes.is_empty(), authority_change);
        prop_assert_eq!(!transition.harness_changes.is_empty(), harness_change);
        if remove {
            let history = transition.removed_capabilities.iter()
                .find(|record| record.capability.capability_id == removable_id)
                .unwrap();
            prop_assert_eq!(
                &history.capability,
                &from.ledger.capabilities[&removable_id]
            );
        }
        let changed_record_has_target_evidence = to
            .ledger
            .capabilities
            .get(&base_id)
            .is_some_and(|record| {
                !record.implementation_evidence.is_empty()
                    || !record.verification_evidence.is_empty()
            });
        if change && changed_record_has_target_evidence {
            let mut stale = to.clone();
            stale.evidence = from.evidence.clone();
            prop_assert!(diff_targets(
                from.as_snapshot(),
                stale.as_snapshot(),
                &CanonicalSet::default(),
            ).is_err());
        }
        if add {
            let mut unclassified = to.clone();
            unclassified.ledger.capabilities.remove(&added_id);
            prop_assert!(diff_targets(
                from.as_snapshot(),
                unclassified.as_snapshot(),
                &CanonicalSet::default(),
            ).is_err());
        }
    }
}

fn spec_reference(locator: &str) -> SpecReference {
    SpecReference {
        path: RepositoryRelativePath::new(
            ".kiro/specs/rust-sdk-complete-implementation/requirements.md",
        )
        .unwrap(),
        locator: SourceLocator::new(locator).unwrap(),
    }
}

// Invariant: reviewed stability, compatibility, experimental, and migration inputs equal the policy table.
// Feature: rust-sdk-completeness-contract, Property 14: stability and migration classification
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_14_stability_and_migration_classification(
        cases in equivalent_contract_cases_strategy(),
        stability_index in 0_usize..3,
        change_index in 0_usize..5,
        user_facing in any::<bool>(),
        has_experimental_condition in any::<bool>(),
        migration_index in 0_usize..3,
    ) {
        let (mut from, mut to) = transition_pair(&cases.forward);
        let capability_id = cases.forward.capability_record.capability_id.clone();
        let removable_id = CapabilityId::new("behavior/transition/removable").unwrap();
        from.inventory.capabilities.remove(&removable_id);
        from.ledger.capabilities.remove(&removable_id);
        to.inventory.capabilities.remove(&removable_id);
        to.ledger.capabilities.remove(&removable_id);

        let stability = [Stability::Stable, Stability::Experimental, Stability::Internal]
            [stability_index]
            .clone();
        let change_kind = [
            RustApiChangeKind::Added,
            RustApiChangeKind::Removed,
            RustApiChangeKind::Compatible,
            RustApiChangeKind::Deprecated,
            RustApiChangeKind::Incompatible,
        ][change_index]
            .clone();
        match change_kind {
            RustApiChangeKind::Added => {
                from.inventory.capabilities.remove(&capability_id);
                from.ledger.capabilities.remove(&capability_id);
                to.inventory.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
                to.ledger.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
            }
            RustApiChangeKind::Removed => {
                from.inventory.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
                from.ledger.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
                to.inventory.capabilities.remove(&capability_id);
                to.ledger.capabilities.remove(&capability_id);
            }
            RustApiChangeKind::Compatible
            | RustApiChangeKind::Deprecated
            | RustApiChangeKind::Incompatible => {
                from.inventory.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
                from.ledger.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
                to.inventory.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
                to.ledger.capabilities.get_mut(&capability_id).unwrap().stability =
                    stability.clone();
                let changed_fingerprint = Digest::sha256("reviewed-api-change");
                to.inventory.capabilities.get_mut(&capability_id).unwrap().capability_fingerprint =
                    changed_fingerprint.clone();
                to.ledger.capabilities.get_mut(&capability_id).unwrap().capability_fingerprint =
                    changed_fingerprint;
            }
        }
        let migration_requirement = match migration_index {
            1 => Some(OwnedSpecReference {
                owner_feature: FeatureId::Feature8,
                reference: spec_reference("feature-8"),
            }),
            2 => Some(OwnedSpecReference {
                owner_feature: FeatureId::Feature9,
                reference: spec_reference("feature-9"),
            }),
            _ => None,
        };
        let review = RustApiTransitionReview {
            capability_id,
            change_kind: change_kind.clone(),
            user_facing,
            experimental_condition: has_experimental_condition
                .then(|| spec_reference("feature-9-experimental-condition")),
            migration_requirement,
        };
        let result = diff_targets(
            from.as_snapshot(),
            to.as_snapshot(),
            &CanonicalSet::new([review]),
        );
        let migration_required = user_facing
            && matches!(
                change_kind,
                RustApiChangeKind::Removed | RustApiChangeKind::Incompatible
            );
        let migration_valid = if migration_required {
            migration_index == 2
        } else {
            migration_index == 0
        };
        let experimental_valid = stability != Stability::Experimental || has_experimental_condition;
        let expected_valid = migration_valid && experimental_valid;
        prop_assert_eq!(result.is_ok(), expected_valid);

        if let Ok(transition) = result {
            let expected_effect = match (&stability, &change_kind) {
                (
                    Stability::Stable | Stability::Experimental,
                    RustApiChangeKind::Added,
                ) => SemverEffect::Additive,
                (Stability::Stable, RustApiChangeKind::Removed) => SemverEffect::Breaking,
                (Stability::Stable, RustApiChangeKind::Deprecated) => SemverEffect::Deprecation,
                (Stability::Stable, RustApiChangeKind::Incompatible) => SemverEffect::Breaking,
                _ => SemverEffect::None,
            };
            prop_assert_eq!(transition.semver_effect, expected_effect);
            prop_assert_eq!(!transition.migration_requirements.is_empty(), migration_required);
        }
    }
}

fn verification_for_target(
    case: &support::contract_case::ContractCase,
    evidence_id: &str,
    target: TargetDigest,
) -> EvidenceReference {
    let mut evidence = case
        .evidence_registry
        .evidence
        .values()
        .find(|reference| reference.evidence_kind == EvidenceKind::Verification)
        .unwrap()
        .clone();
    evidence.evidence_id = EvidenceId::new(evidence_id).unwrap();
    evidence.execution_target = Some(target);
    evidence.expected_outcome.as_mut().unwrap().outcome = CheckOutcome::Passed;
    evidence
}

fn compatibility_fixture(
    case: &support::contract_case::ContractCase,
    ranged: bool,
) -> (
    CompatibilityClaim,
    BTreeMap<TargetDigest, TargetDescriptor>,
    EvidenceRegistry,
    CanonicalInventory,
    ResolvedLedger,
) {
    let mut lower = case.target.clone();
    lower.previous_target = None;
    lower.engine_version = DaggerVersion::new("v1.0.0").unwrap();
    let mut upper = lower.clone();
    upper.engine_version = DaggerVersion::new("v1.1.0").unwrap();
    upper.schema_digest = Digest::sha256("upper-schema");
    let lower_digest = target_digest(&lower);
    let upper_digest = target_digest(&upper);
    let targets = BTreeMap::from([
        (lower_digest.clone(), lower.clone()),
        (upper_digest.clone(), upper.clone()),
    ]);

    let lower_evidence = verification_for_target(
        case,
        "verification/compatibility-lower",
        lower_digest.clone(),
    );
    let upper_evidence = verification_for_target(
        case,
        "verification/compatibility-upper",
        upper_digest.clone(),
    );
    let evidence = EvidenceRegistry {
        evidence: BTreeMap::from([
            (lower_evidence.evidence_id.clone(), lower_evidence.clone()),
            (upper_evidence.evidence_id.clone(), upper_evidence.clone()),
        ]),
    };
    let supported_targets = if ranged {
        SupportedTargets::InclusiveRange(InclusiveTargetRange {
            lower: lower_digest.clone(),
            upper: upper_digest.clone(),
        })
    } else {
        SupportedTargets::Exact(CanonicalSet::new([upper_digest.clone()]))
    };
    let range_boundaries = if ranged {
        CanonicalSet::new([lower_digest, upper_digest])
    } else {
        CanonicalSet::new([upper_digest])
    };
    let conformance_evidence = if ranged {
        CanonicalSet::new([lower_evidence.evidence_id, upper_evidence.evidence_id])
    } else {
        CanonicalSet::new([upper_evidence.evidence_id])
    };
    let outside_range_capability = case.capability_record.capability_id.clone();
    let claim_digest = canonical_digest(
        DigestDomain::Compatibility,
        &(
            upper.rust_sdk_version.clone(),
            supported_targets.clone(),
            range_boundaries.clone(),
            conformance_evidence.clone(),
            outside_range_capability.clone(),
        ),
    )
    .unwrap();
    let claim = CompatibilityClaim {
        rust_sdk_version: upper.rust_sdk_version,
        supported_targets,
        range_boundaries,
        conformance_evidence,
        outside_range_capability: outside_range_capability.clone(),
        claim_digest,
    };
    let inventory = case.inventory.clone();
    let mut ledger = case.ledger.clone();
    let outside = ledger
        .capabilities
        .get_mut(&outside_range_capability)
        .unwrap();
    if matches!(outside.status, Status::Missing | Status::Partial) {
        outside.owner_feature = Some(FeatureId::Feature2);
    }
    (claim, targets, evidence, inventory, ledger)
}

// Invariant: exact/range claims pass exactly when targets, evidence, response, and digest agree.
// Feature: rust-sdk-completeness-contract, Property 19: compatibility-claim truthfulness
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_19_compatibility_claim_truthfulness(
        cases in equivalent_contract_cases_strategy(),
        ranged in any::<bool>(),
        mutation in 0_u8..6,
    ) {
        let (mut claim, mut targets, mut evidence, mut inventory, ledger) =
            compatibility_fixture(&cases.forward, ranged);
        match mutation {
            1 => claim.range_boundaries = CanonicalSet::default(),
            2 => evidence.evidence.clear(),
            3 => {
                inventory.capabilities.remove(&claim.outside_range_capability);
            }
            4 => claim.claim_digest = Digest::sha256("incorrect-claim"),
            5 if ranged => {
                let SupportedTargets::InclusiveRange(range) = &mut claim.supported_targets else {
                    unreachable!();
                };
                std::mem::swap(&mut range.lower, &mut range.upper);
                claim.range_boundaries = CanonicalSet::new([range.lower.clone(), range.upper.clone()]);
                claim.claim_digest = canonical_digest(
                    DigestDomain::Compatibility,
                    &(
                        claim.rust_sdk_version.clone(),
                        claim.supported_targets.clone(),
                        claim.range_boundaries.clone(),
                        claim.conformance_evidence.clone(),
                        claim.outside_range_capability.clone(),
                    ),
                ).unwrap();
            }
            5 => targets.clear(),
            _ => {}
        }
        let result = validate_compatibility_claim(
            claim.clone(),
            &targets,
            &evidence,
            &inventory,
            &ledger,
        );
        prop_assert_eq!(result.is_ok(), mutation == 0);
        if let Ok(validated) = result {
            let release = validated.release_metadata();
            prop_assert_eq!(release.rust_sdk_version, claim.rust_sdk_version);
            prop_assert_eq!(release.supported_targets, claim.supported_targets);
            prop_assert_eq!(release.claim_digest, claim.claim_digest);
        }
    }
}

fn report_fixture(
    case: &support::contract_case::ContractCase,
    statuses: &[u8],
    has_diagnostic: bool,
) -> (
    CompletenessReport,
    BTreeMap<Status, u64>,
    BTreeMap<FeatureId, u64>,
) {
    let authority_id = case.capability_record.authority_id.clone();
    let mut inventory = CanonicalInventory::default();
    let mut ledger = ResolvedLedger::default();
    let mut expected_statuses = [
        Status::Missing,
        Status::Partial,
        Status::Implemented,
        Status::IdiomaticEquivalent,
        Status::Inapplicable,
    ]
    .into_iter()
    .map(|status| (status, 0))
    .collect::<BTreeMap<_, _>>();
    let mut expected_owners = [
        FeatureId::Feature2,
        FeatureId::Feature3,
        FeatureId::Feature4,
        FeatureId::Feature5,
        FeatureId::Feature6,
        FeatureId::Feature7,
        FeatureId::Feature8,
        FeatureId::Feature9,
    ]
    .into_iter()
    .map(|feature| (feature, 0))
    .collect::<BTreeMap<_, _>>();

    for (index, status_index) in statuses.iter().enumerate() {
        let capability_id =
            CapabilityId::new(format!("behavior/report/capability-{index}")).unwrap();
        let status = [
            Status::Missing,
            Status::Partial,
            Status::Implemented,
            Status::IdiomaticEquivalent,
            Status::Inapplicable,
        ][usize::from(*status_index)]
        .clone();
        let mut definition = case.capability_definition.clone();
        definition.capability_id = capability_id.clone();
        definition.authority_id = authority_id.clone();
        definition.capability_kind = CapabilityKind::new(if index % 2 == 0 {
            "behavior/report-even"
        } else {
            "behavior/report-odd"
        })
        .unwrap();
        definition.capability_fingerprint = Digest::sha256(format!("report-{index}"));
        let mut record = case.capability_record.clone();
        record.capability_id = capability_id.clone();
        record.authority_id = authority_id.clone();
        record.capability_kind = definition.capability_kind.clone();
        record.capability_fingerprint = definition.capability_fingerprint.clone();
        record.status = status.clone();
        record.owner_feature = if matches!(status, Status::Missing | Status::Partial) {
            let owner = [
                FeatureId::Feature2,
                FeatureId::Feature3,
                FeatureId::Feature4,
                FeatureId::Feature5,
                FeatureId::Feature6,
                FeatureId::Feature7,
                FeatureId::Feature8,
                FeatureId::Feature9,
            ][index % 8]
                .clone();
            *expected_owners.get_mut(&owner).unwrap() += 1;
            Some(owner)
        } else {
            None
        };
        if matches!(status, Status::IdiomaticEquivalent | Status::Inapplicable) {
            record.decision_evidence =
                CanonicalSet::new([EvidenceId::new(format!("decision/report-{index}")).unwrap()]);
        }
        *expected_statuses.get_mut(&status).unwrap() += 1;
        inventory
            .capabilities
            .insert(capability_id.clone(), definition);
        ledger.capabilities.insert(capability_id, record);
    }

    let diagnostics = has_diagnostic.then(|| {
        ContractDiagnostic::new(
            DiagnosticCode::LedgerDrift,
            "report-fixture",
            None,
            "generated integrity defect",
        )
    });
    let report = build_report(
        case.target.contract_format_version.clone(),
        &case.target,
        &case.authority_registry,
        &inventory,
        &ledger,
        diagnostics,
    );
    (report, expected_statuses, expected_owners)
}

// Invariant: the report equals direct reference counts and the two independent verdict equations.
// Feature: rust-sdk-completeness-contract, Property 15: verdict and report aggregation
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_15_verdict_and_report_aggregation(
        cases in equivalent_contract_cases_strategy(),
        statuses in prop::collection::vec(0_u8..5, 0..16),
        has_diagnostic in any::<bool>(),
    ) {
        let (report, expected_statuses, expected_owners) =
            report_fixture(&cases.forward, &statuses, has_diagnostic);
        let blocking = statuses.iter().any(|status| matches!(status, 0 | 1));

        prop_assert_eq!(&report.counts_by_status, &expected_statuses);
        prop_assert_eq!(&report.counts_by_owner, &expected_owners);
        prop_assert_eq!(report.integrity_verdict, !has_diagnostic);
        prop_assert_eq!(report.completeness_verdict, !has_diagnostic && !blocking);
        prop_assert_eq!(report.blocking_capabilities.len(), statuses.iter()
            .filter(|status| matches!(status, 0 | 1)).count());
        prop_assert_eq!(report.complete_exceptions.len(), statuses.iter()
            .filter(|status| matches!(status, 3 | 4)).count());

        let human = render_human_report(&report);
        let decoded: CompletenessReport = decode_canonical(&canonical_bytes(&report).unwrap()).unwrap();
        prop_assert_eq!(render_human_report(&decoded), human.as_str());
        for status in ["Missing", "Partial", "Implemented", "Idiomatic_Equivalent", "Inapplicable"] {
            prop_assert!(human.contains(status));
        }
    }
}

// Invariant: CLI success is exactly the selected verdict and named profiles never swap gates.
// Feature: rust-sdk-completeness-contract, Property 16: gate selection
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_16_gate_selection(
        cases in equivalent_contract_cases_strategy(),
        integrity in any::<bool>(),
        complete_when_integral in any::<bool>(),
        select_completeness in any::<bool>(),
    ) {
        let mut report = cases.forward.report;
        report.integrity_verdict = integrity;
        report.completeness_verdict = integrity && complete_when_integral;
        let gate = if select_completeness { Gate::Completeness } else { Gate::Integrity };
        let expected = if select_completeness {
            report.completeness_verdict
        } else {
            report.integrity_verdict
        };

        prop_assert_eq!(gate_passes(&report, gate), expected);
        prop_assert_eq!(gate_exit_status(&report, gate), if expected { 0 } else { 1 });
        prop_assert_eq!(profile_gate(GateProfile::InitialCi), Gate::Integrity);
        prop_assert_eq!(profile_gate(GateProfile::Feature9Release), Gate::Completeness);
    }
}

fn snapshot_tree(root: &Path) -> BTreeMap<PathBuf, Vec<u8>> {
    fn visit(root: &Path, current: &Path, snapshot: &mut BTreeMap<PathBuf, Vec<u8>>) {
        for entry in fs::read_dir(current).unwrap() {
            let path = entry.unwrap().path();
            if path.is_dir() {
                visit(root, &path, snapshot);
            } else {
                snapshot.insert(
                    path.strip_prefix(root).unwrap().to_path_buf(),
                    fs::read(path).unwrap(),
                );
            }
        }
    }
    let mut snapshot = BTreeMap::new();
    visit(root, root, &mut snapshot);
    snapshot
}

// Invariant: every rejected mutating command preserves every byte beneath the active root.
// Feature: rust-sdk-completeness-contract, Property 20: rejection is artifact-preserving
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_20_rejection_is_artifact_preserving(
        cases in equivalent_contract_cases_strategy(),
        command_index in 0_u8..3,
        active_bytes in prop::collection::vec(any::<u8>(), 0..128),
    ) {
        let temporary = tempfile::tempdir().unwrap();
        let repository = temporary.path().join("repository");
        let active = repository.join("sdk/rust/completeness");
        let mut report = build_report(
            cases.forward.target.contract_format_version.clone(),
            &cases.forward.target,
            &cases.forward.authority_registry,
            &cases.forward.inventory,
            &cases.forward.ledger,
            [],
        );
        if command_index == 0 {
            report.inventory_digest = Digest::sha256("rejected-report-digest");
        }
        materialize_contract(&active, &cases.forward, &report);
        fs::create_dir_all(active.join("opaque")).unwrap();
        fs::write(active.join("opaque/reviewer-owned.bin"), &active_bytes).unwrap();

        let candidate = temporary.path().join("candidate");
        let mut candidate_target = cases.forward.target.clone();
        candidate_target.schema_digest = Digest::sha256("invalid-candidate-schema");
        candidate_target.previous_target = Some(TargetDigest::new(Digest::sha256("wrong-base")));
        materialize_candidate(&candidate, &cases.forward, &candidate_target);
        let candidate_path = candidate.join("target.json");

        let mut invalid_run = cases.forward.harness_result.clone();
        invalid_run.assertion = NonEmptyText::new("different assertion identity").unwrap();
        let run_path = temporary.path().join("invalid-run.json");
        write_contract_artifact(&run_path, &invalid_run);
        let before = snapshot_tree(&active);

        let backend = ArtifactCliBackend;
        let command = match command_index {
            0 => "render",
            1 => "transition",
            _ => "import-evidence",
        };
        let mut stderr_runs = Vec::new();
        for run_index in 0..2 {
            let output = temporary.path().join(format!("output-{run_index}"));
            let mut arguments = vec![
                "dagger-sdk-completeness".to_owned(),
                command.to_owned(),
                "--root".to_owned(),
                repository.to_string_lossy().into_owned(),
            ];
            if command == "transition" {
                arguments.extend([
                    "--candidate".to_owned(),
                    candidate_path.to_string_lossy().into_owned(),
                ]);
            } else if command == "import-evidence" {
                arguments.extend([
                    "--run".to_owned(),
                    run_path.to_string_lossy().into_owned(),
                ]);
            }
            arguments.extend(["--output".to_owned(), output.to_string_lossy().into_owned()]);
            let mut stdout = Vec::new();
            let mut stderr = Vec::new();
            let status = run_with_backend(arguments, &backend, &mut stdout, &mut stderr);
            prop_assert_eq!(status, 1);
            prop_assert!(!stderr.is_empty());
            stderr_runs.push(stderr);
        }

        prop_assert_eq!(snapshot_tree(&active), before);
        prop_assert_eq!(&stderr_runs[0], &stderr_runs[1]);
    }
}

#[test]
fn staging_refuses_existing_content_and_writes_within_its_root() {
    let temporary = tempfile::tempdir().unwrap();
    let non_empty = temporary.path().join("non-empty");
    fs::create_dir(&non_empty).unwrap();
    fs::write(non_empty.join("owned.txt"), b"owned").unwrap();
    assert!(IsolatedStaging::prepare(&non_empty).is_err());
    assert_eq!(fs::read(non_empty.join("owned.txt")).unwrap(), b"owned");

    let output = temporary.path().join("output");
    let staging = IsolatedStaging::prepare(&output).unwrap();
    staging
        .write(
            &RepositoryRelativePath::new("artifacts/report.md").unwrap(),
            b"report\n",
        )
        .unwrap();
    assert_eq!(
        fs::read(output.join("artifacts/report.md")).unwrap(),
        b"report\n"
    );
}

fn write_contract_artifact(path: &Path, value: &impl Serialize) {
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, canonical_bytes(value).unwrap()).unwrap();
}

fn materialize_contract(
    contract: &Path,
    case: &support::contract_case::ContractCase,
    report: &CompletenessReport,
) {
    materialize_candidate(contract, case, &case.target);
    write_contract_artifact(&contract.join("artifacts/report.json"), report);
    fs::write(
        contract.join("artifacts/report.md"),
        render_human_report(report),
    )
    .unwrap();
}

fn materialize_candidate(
    root: &Path,
    case: &support::contract_case::ContractCase,
    target: &TargetDescriptor,
) {
    write_contract_artifact(&root.join("target.json"), target);
    write_contract_artifact(&root.join("authorities.json"), &case.authority_registry);
    write_contract_artifact(&root.join("artifacts/inventory.json"), &case.inventory);
    write_contract_artifact(&root.join("artifacts/ledger.json"), &case.ledger);
    write_contract_artifact(
        &root.join("evidence/registry.json"),
        &case.evidence_registry,
    );
    write_contract_artifact(&root.join("harness-mappings.json"), &case.harness_mappings);
}

#[test]
fn artifact_backend_exercises_every_reviewed_command_surface() {
    let mut runner = TestRunner::deterministic();
    let case = equivalent_contract_cases_strategy()
        .new_tree(&mut runner)
        .unwrap()
        .current()
        .forward;
    let temporary = tempfile::tempdir().unwrap();
    let repository = temporary.path().join("repository");
    let contract = repository.join("sdk/rust/completeness");
    let report = build_report(
        case.target.contract_format_version.clone(),
        &case.target,
        &case.authority_registry,
        &case.inventory,
        &case.ledger,
        [],
    );
    materialize_contract(&contract, &case, &report);
    let active_before = snapshot_tree(&contract);

    let backend = ArtifactCliBackend;
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    let verify_status = run_with_backend(
        [
            "dagger-sdk-completeness",
            "verify",
            "--root",
            repository.to_str().unwrap(),
            "--gate",
            "integrity",
            "--format",
            "human",
        ],
        &backend,
        &mut stdout,
        &mut stderr,
    );
    assert_eq!(verify_status, 0);
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        render_human_report(&report)
    );
    assert!(stderr.is_empty());
    assert_eq!(snapshot_tree(&contract), active_before);

    let forbidden_output = contract.join("staged-output");
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    assert_eq!(
        run_with_backend(
            [
                "dagger-sdk-completeness",
                "render",
                "--root",
                repository.to_str().unwrap(),
                "--output",
                forbidden_output.to_str().unwrap(),
            ],
            &backend,
            &mut stdout,
            &mut stderr,
        ),
        2
    );
    assert!(!forbidden_output.exists());
    assert_eq!(snapshot_tree(&contract), active_before);

    let render_output = temporary.path().join("render-output");
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    assert_eq!(
        run_with_backend(
            [
                "dagger-sdk-completeness",
                "render",
                "--root",
                repository.to_str().unwrap(),
                "--output",
                render_output.to_str().unwrap(),
            ],
            &backend,
            &mut stdout,
            &mut stderr,
        ),
        0
    );
    assert!(render_output.join("artifacts/report.json").is_file());
    assert!(render_output.join("artifacts/report.md").is_file());
    let second_render_output = temporary.path().join("distinct/absolute/render-output");
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    assert_eq!(
        run_with_backend(
            [
                "dagger-sdk-completeness",
                "render",
                "--root",
                repository.to_str().unwrap(),
                "--output",
                second_render_output.to_str().unwrap(),
            ],
            &backend,
            &mut stdout,
            &mut stderr,
        ),
        0
    );
    assert_eq!(
        snapshot_tree(&render_output),
        snapshot_tree(&second_render_output)
    );

    let candidate = temporary.path().join("candidate");
    let mut candidate_target = case.target.clone();
    candidate_target.schema_digest = Digest::sha256("candidate-schema");
    candidate_target.previous_target = Some(target_digest(&case.target));
    materialize_candidate(&candidate, &case, &candidate_target);
    let transition_output = temporary.path().join("transition-output");
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    assert_eq!(
        run_with_backend(
            [
                "dagger-sdk-completeness",
                "transition",
                "--root",
                repository.to_str().unwrap(),
                "--candidate",
                candidate.join("target.json").to_str().unwrap(),
                "--output",
                transition_output.to_str().unwrap(),
            ],
            &backend,
            &mut stdout,
            &mut stderr,
        ),
        0
    );
    assert_eq!(
        fs::read_dir(transition_output.join("transitions"))
            .unwrap()
            .count(),
        1
    );

    let run_path = temporary.path().join("run.json");
    write_contract_artifact(&run_path, &case.harness_result);
    let import_output = temporary.path().join("import-output");
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    assert_eq!(
        run_with_backend(
            [
                "dagger-sdk-completeness",
                "import-evidence",
                "--root",
                repository.to_str().unwrap(),
                "--run",
                run_path.to_str().unwrap(),
                "--output",
                import_output.to_str().unwrap(),
            ],
            &backend,
            &mut stdout,
            &mut stderr,
        ),
        0
    );
    assert!(import_output.join("evidence/runs").is_dir());
    assert_eq!(snapshot_tree(&contract), active_before);
}
