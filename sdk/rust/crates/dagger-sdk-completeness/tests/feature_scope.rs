//! Feature 2 scope, routing-preservation, and evidence-closure properties.
//!
//! The reference models here are intentionally smaller than the production contract. They build
//! accepted values first and then mutate one reviewed boundary, so a failure identifies scope,
//! preservation, evidence, or blocker logic rather than unrelated malformed scaffolding.

use std::collections::BTreeMap;

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const REQUIREMENTS: &str =
    include_str!("../../../../../.kiro/specs/rust-sdk-client-lifecycle/requirements.md");
const CASES: u32 = 256;

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/feature-scope.txt"
        )))),
        ..Config::default()
    }
}

// Invariant: accepted scope prose and ownership corrections have exactly one reviewed meaning.
// Feature: rust-sdk-client-lifecycle, Property 1: exact feature scope and routing preservation
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_1_exact_feature_scope_and_routing_preservation(
        scope_mutation in 0_u8..6,
        routing_mutation in 0_u8..7,
        outsider_status in 0_u8..5,
        fingerprint_seed in any::<u64>(),
    ) {
        let markdown = mutate_scope(REQUIREMENTS, scope_mutation);
        let declaration = parse_feature_scope_declaration(&markdown);
        prop_assert_eq!(declaration.is_ok(), scope_mutation == 0);
        if scope_mutation != 0 {
            return Ok(());
        }
        let declaration = declaration.unwrap();
        let (inventory, before, mut after) = routing_fixture(
            &declaration,
            status_from_index(outsider_status),
            fingerprint_seed,
        );
        let outsider = CapabilityId::new("behavior/go-client/outside-scope").unwrap();
        match routing_mutation {
            1 => {
                let row = after.capabilities.get_mut(&outsider).unwrap();
                row.status = if row.status == Status::Missing {
                    Status::Partial
                } else {
                    Status::Missing
                };
            }
            2 => {
                after.capabilities.get_mut(&outsider).unwrap().capability_fingerprint =
                    Digest::sha256("changed fingerprint");
            }
            3 => {
                after.capabilities.get_mut(&outsider).unwrap().implementation_evidence =
                    CanonicalSet::new([EvidenceId::new("implementation/changed").unwrap()]);
            }
            4 => after.capabilities.get_mut(&outsider).unwrap().source_anchors =
                CanonicalSet::default(),
            5 => {
                let row = after.capabilities.get_mut(&outsider).unwrap();
                row.status = Status::Partial;
                row.owner_feature = Some(FeatureId::Feature2);
            }
            6 => {
                let scoped = declaration.existing_capability_ids.first().unwrap();
                after.capabilities.get_mut(scoped).unwrap().owner_feature =
                    Some(FeatureId::Feature3);
            }
            _ => {}
        }

        let preservation =
            validate_ownership_only_correction(&before, &after, &declaration).is_ok();
        let routing = validate_feature_scope_routing(&inventory, &after, &declaration).is_ok();
        prop_assert_eq!(preservation && routing, routing_mutation == 0);
    }
}

// Invariant: complete status and an exact sibling blocker are mutually exclusive evidence states.
// Feature: rust-sdk-client-lifecycle, Property 2: complete status is evidence-closed
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_2_complete_status_is_evidence_closed(
        destination_index in 0_u8..4,
        mutation in 0_u8..6,
    ) {
        let fixture = status_fixture(destination_index);
        let mut candidate = fixture.candidate;
        let mut evidence = fixture.evidence;
        let mut blockers = fixture.blockers;
        match mutation {
            1 => {
                if destination_index == 3 {
                    evidence
                        .evidence
                        .get_mut(&EvidenceId::new("implementation/client").unwrap())
                        .unwrap()
                        .evidence_kind = EvidenceKind::Verification;
                } else {
                    let id = if destination_index == 2 {
                        "decision/client"
                    } else {
                        "implementation/client"
                    };
                    evidence
                        .evidence
                        .get_mut(&EvidenceId::new(id).unwrap())
                        .unwrap()
                        .execution_target = Some(TargetDigest::new(Digest::sha256("other")));
                }
            }
            2 => {
                if destination_index == 3 {
                    candidate
                        .changes
                        .get_mut(&fixture.capability_id)
                        .unwrap()
                        .owner_feature = Some(FeatureId::Feature3);
                } else {
                    blockers.insert(
                        fixture.capability_id.clone(),
                        ResidualBlocker {
                            sibling_feature: FeatureId::Feature3,
                            gap: NonEmptyText::new("Feature 3 transport is unverified").unwrap(),
                        },
                    );
                }
            }
            3 => {
                let values = candidate.changes.get_mut(&fixture.capability_id).unwrap();
                match values.status {
                    Status::Inapplicable => values.decision_evidence = CanonicalSet::default(),
                    Status::Partial => values.implementation_evidence = CanonicalSet::default(),
                    Status::Implemented | Status::IdiomaticEquivalent => {
                        values.verification_evidence = CanonicalSet::default();
                    }
                    Status::Missing => unreachable!(),
                }
            }
            4 => {
                let id = match destination_index {
                    2 => "decision/client",
                    3 => "implementation/client",
                    _ => "verification/client",
                };
                evidence
                    .evidence
                    .get_mut(&EvidenceId::new(id).unwrap())
                    .unwrap()
                    .proved_capability_ids = CanonicalSet::default();
            }
            5 => {
                let values = candidate.changes.remove(&fixture.capability_id).unwrap();
                candidate.changes.insert(
                    CapabilityId::new("behavior/go-client/undeclared").unwrap(),
                    values,
                );
            }
            _ => {}
        }

        let require_no_blockers = destination_index != 3;
        let result = validate_feature_status_changes(
            &fixture.current,
            &fixture.declaration,
            &candidate,
            &evidence,
            &fixture.target,
            &blockers,
            require_no_blockers,
        );
        prop_assert_eq!(result.is_ok(), mutation == 0);
    }
}

struct StatusFixture {
    capability_id: CapabilityId,
    declaration: FeatureScopeDeclaration,
    current: ResolvedLedger,
    candidate: CandidateStatusChanges,
    evidence: EvidenceRegistry,
    target: TargetDigest,
    blockers: BTreeMap<CapabilityId, ResidualBlocker>,
}

fn status_fixture(destination_index: u8) -> StatusFixture {
    let capability_id = CapabilityId::new("behavior/go-client/connect").unwrap();
    let target = TargetDigest::new(Digest::sha256("target"));
    let definition = definition(&capability_id, "go-client", Digest::sha256("connect"));
    let current_values = blocking_values(Status::Missing, "Feature 2 client is absent");
    let current = ResolvedLedger {
        capabilities: BTreeMap::from([(
            capability_id.clone(),
            record(&definition, &current_values),
        )]),
    };
    let destination = match destination_index {
        0 => Status::Implemented,
        1 => Status::IdiomaticEquivalent,
        2 => Status::Inapplicable,
        _ => Status::Partial,
    };
    let replacement = destination_values(destination);
    let blockers = if destination_index == 3 {
        BTreeMap::from([(
            capability_id.clone(),
            ResidualBlocker {
                sibling_feature: FeatureId::Feature3,
                gap: NonEmptyText::new("Feature 3 transport is unverified").unwrap(),
            },
        )])
    } else {
        BTreeMap::new()
    };
    StatusFixture {
        capability_id: capability_id.clone(),
        declaration: FeatureScopeDeclaration {
            feature: FeatureId::Feature2,
            existing_capability_ids: CanonicalSet::new([capability_id.clone()]),
            existing_scope_digest: Digest::sha256("fixture scope"),
            policy_capability_ids: CanonicalSet::default(),
        },
        current,
        candidate: CandidateStatusChanges {
            changes: BTreeMap::from([(capability_id.clone(), replacement)]),
        },
        evidence: status_evidence(&capability_id, &target),
        target,
        blockers,
    }
}

fn destination_values(status: Status) -> ClassificationValues {
    let implementation = EvidenceId::new("implementation/client").unwrap();
    let verification = EvidenceId::new("verification/client").unwrap();
    let decision = EvidenceId::new("decision/client").unwrap();
    match status {
        Status::Implemented => ClassificationValues {
            status,
            gap: None,
            owner_feature: None,
            implementation_evidence: CanonicalSet::new([implementation]),
            verification_evidence: CanonicalSet::new([verification]),
            decision_evidence: CanonicalSet::default(),
        },
        Status::IdiomaticEquivalent => ClassificationValues {
            status,
            gap: None,
            owner_feature: None,
            implementation_evidence: CanonicalSet::new([implementation]),
            verification_evidence: CanonicalSet::new([verification]),
            decision_evidence: CanonicalSet::new([decision]),
        },
        Status::Inapplicable => ClassificationValues {
            status,
            gap: None,
            owner_feature: None,
            implementation_evidence: CanonicalSet::default(),
            verification_evidence: CanonicalSet::default(),
            decision_evidence: CanonicalSet::new([decision]),
        },
        Status::Partial => {
            let mut values = blocking_values(status, "Feature 3 transport is unverified");
            values.implementation_evidence = CanonicalSet::new([implementation]);
            values
        }
        Status::Missing => unreachable!(),
    }
}

fn status_evidence(capability_id: &CapabilityId, target: &TargetDigest) -> EvidenceRegistry {
    let repository = RepositoryId::new("github.com/dagger/dagger").unwrap();
    let revision = CommitSha::new("a".repeat(40)).unwrap();
    let implementation = evidence(
        "implementation/client",
        EvidenceKind::Implementation,
        capability_id,
        target,
        &repository,
        &revision,
        "sdk/rust/crates/dagger-sdk/src/client.rs",
        "implements the owned client",
        false,
    );
    let verification = evidence(
        "verification/client",
        EvidenceKind::Verification,
        capability_id,
        target,
        &repository,
        &revision,
        "sdk/rust/crates/dagger-sdk/tests/client.rs",
        "verifies the owned client",
        true,
    );
    let decision = evidence(
        "decision/client",
        EvidenceKind::Decision,
        capability_id,
        target,
        &repository,
        &revision,
        ".kiro/specs/rust-sdk-client-lifecycle/design.md",
        "no meaningful Rust counterpart exists for this language-specific mechanism",
        false,
    );
    EvidenceRegistry {
        evidence: BTreeMap::from([
            (implementation.evidence_id.clone(), implementation),
            (verification.evidence_id.clone(), verification),
            (decision.evidence_id.clone(), decision),
        ]),
    }
}

#[allow(clippy::too_many_arguments)]
fn evidence(
    id: &str,
    kind: EvidenceKind,
    capability_id: &CapabilityId,
    target: &TargetDigest,
    repository: &RepositoryId,
    revision: &CommitSha,
    path: &str,
    claim: &str,
    executable: bool,
) -> EvidenceReference {
    EvidenceReference {
        evidence_id: EvidenceId::new(id).unwrap(),
        evidence_kind: kind,
        repository: repository.clone(),
        revision: revision.clone(),
        path: RepositoryRelativePath::new(path).unwrap(),
        locator: SourceLocator::new(format!("{path}#fixture")).unwrap(),
        claim: NonEmptyText::new(claim).unwrap(),
        command: executable.then(|| CommandSpec {
            program: ExecutableId::new("cargo").unwrap(),
            args: vec!["test".to_owned(), "--locked".to_owned()],
            working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
            environment: BTreeMap::new(),
        }),
        expected_outcome: executable.then(|| ExpectedOutcome {
            outcome: CheckOutcome::Passed,
            assertion: NonEmptyText::new("client behaviour passes").unwrap(),
        }),
        execution_target: Some(target.clone()),
        platform_scope: if executable {
            CanonicalSet::new([Platform {
                operating_system: OperatingSystem::Linux,
                architecture: Architecture::Amd64,
            }])
        } else {
            CanonicalSet::default()
        },
        proved_capability_ids: CanonicalSet::new([capability_id.clone()]),
    }
}

fn routing_fixture(
    declaration: &FeatureScopeDeclaration,
    outsider_status: Status,
    fingerprint_seed: u64,
) -> (CanonicalInventory, ResolvedLedger, ResolvedLedger) {
    let mut definitions = BTreeMap::new();
    let mut before_rows = BTreeMap::new();
    let mut after_rows = BTreeMap::new();
    for capability_id in declaration.existing_capability_ids.iter() {
        let definition = definition(
            capability_id,
            "go-client",
            Digest::sha256(capability_id.as_str()),
        );
        let values = blocking_values(Status::Partial, "Feature 2 remains unverified");
        before_rows.insert(capability_id.clone(), record(&definition, &values));
        after_rows.insert(capability_id.clone(), record(&definition, &values));
        definitions.insert(capability_id.clone(), definition);
    }
    for capability_id in declaration.policy_capability_ids.iter() {
        let definition = definition(
            capability_id,
            "rust-policy",
            Digest::sha256(capability_id.as_str()),
        );
        let values = blocking_values(Status::Missing, "Feature 2 policy remains unimplemented");
        after_rows.insert(capability_id.clone(), record(&definition, &values));
        definitions.insert(capability_id.clone(), definition);
    }

    let outsider = CapabilityId::new("behavior/go-client/outside-scope").unwrap();
    let outsider_definition = definition(
        &outsider,
        "go-client",
        Digest::sha256(fingerprint_seed.to_le_bytes()),
    );
    let mut before_values = values_for_outsider(&outsider_status, FeatureId::Feature2);
    let after_values = values_for_outsider(&outsider_status, FeatureId::Feature3);
    if !matches!(outsider_status, Status::Missing | Status::Partial) {
        before_values.owner_feature = None;
    }
    before_rows.insert(
        outsider.clone(),
        record(&outsider_definition, &before_values),
    );
    after_rows.insert(
        outsider.clone(),
        record(&outsider_definition, &after_values),
    );
    definitions.insert(outsider, outsider_definition);

    (
        CanonicalInventory {
            capabilities: definitions,
        },
        ResolvedLedger {
            capabilities: before_rows,
        },
        ResolvedLedger {
            capabilities: after_rows,
        },
    )
}

fn definition(
    capability_id: &CapabilityId,
    authority: &str,
    fingerprint: Digest,
) -> CapabilityDefinition {
    let authority_id = AuthorityId::new(authority).unwrap();
    let source_id = SourceItemId::new(format!("source/{authority}/fixture")).unwrap();
    let signature = serde_json::json!({"capability": capability_id});
    CapabilityDefinition {
        capability_id: capability_id.clone(),
        authority_id,
        capability_kind: CapabilityKind::new("fixture").unwrap(),
        source_item_ids: CanonicalSet::new([source_id]),
        source_anchors: CanonicalSet::new([authority_anchor(capability_id, authority)]),
        summary: NonEmptyText::new("Feature scope fixture").unwrap(),
        semantic_signature: signature,
        capability_fingerprint: fingerprint,
        stability: Stability::Stable,
    }
}

fn authority_anchor(capability_id: &CapabilityId, authority: &str) -> EvidenceReference {
    EvidenceReference {
        evidence_id: EvidenceId::new(format!("authority/{capability_id}")).unwrap(),
        evidence_kind: EvidenceKind::Authority,
        repository: RepositoryId::new("github.com/dagger/dagger").unwrap(),
        revision: CommitSha::new("a".repeat(40)).unwrap(),
        path: RepositoryRelativePath::new(format!("sources/{authority}.md")).unwrap(),
        locator: SourceLocator::new(format!("sources/{authority}.md#fixture")).unwrap(),
        claim: NonEmptyText::new("defines the fixture capability").unwrap(),
        command: None,
        expected_outcome: None,
        execution_target: None,
        platform_scope: CanonicalSet::default(),
        proved_capability_ids: CanonicalSet::new([capability_id.clone()]),
    }
}

fn values_for_outsider(status: &Status, owner: FeatureId) -> ClassificationValues {
    if matches!(status, Status::Missing | Status::Partial) {
        let mut values = blocking_values(status.clone(), "Sibling feature remains unverified");
        values.owner_feature = Some(owner);
        if *status == Status::Partial {
            values.implementation_evidence =
                CanonicalSet::new([EvidenceId::new("implementation/baseline").unwrap()]);
        }
        values
    } else {
        let inapplicable = *status == Status::Inapplicable;
        ClassificationValues {
            status: status.clone(),
            gap: None,
            owner_feature: None,
            implementation_evidence: if inapplicable {
                CanonicalSet::default()
            } else {
                CanonicalSet::new([EvidenceId::new("implementation/baseline").unwrap()])
            },
            verification_evidence: if inapplicable {
                CanonicalSet::default()
            } else {
                CanonicalSet::new([EvidenceId::new("verification/baseline").unwrap()])
            },
            decision_evidence: matches!(status, Status::IdiomaticEquivalent | Status::Inapplicable)
                .then(|| CanonicalSet::new([EvidenceId::new("decision/baseline").unwrap()]))
                .unwrap_or_default(),
        }
    }
}

fn blocking_values(status: Status, gap: &str) -> ClassificationValues {
    ClassificationValues {
        status,
        gap: Some(NonEmptyText::new(gap).unwrap()),
        owner_feature: Some(FeatureId::Feature2),
        implementation_evidence: CanonicalSet::default(),
        verification_evidence: CanonicalSet::default(),
        decision_evidence: CanonicalSet::default(),
    }
}

fn record(definition: &CapabilityDefinition, values: &ClassificationValues) -> CapabilityRecord {
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

fn status_from_index(index: u8) -> Status {
    [
        Status::Missing,
        Status::Partial,
        Status::Implemented,
        Status::IdiomaticEquivalent,
        Status::Inapplicable,
    ][usize::from(index)]
    .clone()
}

fn mutate_scope(markdown: &str, mutation: u8) -> String {
    let valid = parse_feature_scope_declaration(markdown).unwrap();
    let first = valid.existing_capability_ids[0].as_str();
    let second = valid.existing_capability_ids[1].as_str();
    match mutation {
        1 => markdown.replacen(first, &format!("{first}\n{first}"), 1),
        2 => markdown.replacen(
            &format!("{first}\n{second}"),
            &format!("{second}\n{first}"),
            1,
        ),
        3 => markdown.replacen(first, "Behavior/not-canonical", 1),
        4 => {
            let last = valid.policy_capability_ids.last().unwrap().as_str();
            markdown.replacen(
                last,
                &format!("{last}\npolicy/rust-policy/client-unreviewed"),
                1,
            )
        }
        5 => markdown.replacen(
            valid.existing_scope_digest.as_str(),
            "sha256:91ad1a4f2efe1604b9091468bd6a6006d598a2a8ae54a94a974acf08d74b8b40",
            1,
        ),
        _ => markdown.to_owned(),
    }
}
