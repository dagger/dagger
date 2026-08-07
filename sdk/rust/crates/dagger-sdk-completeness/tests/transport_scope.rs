//! Exact transport-scope and evidence-transition contract tests.
//!
//! The reference fixtures construct accepted declarations and transitions independently, then
//! mutate one reviewed boundary. This keeps a production parser or routing defect from certifying
//! itself through shared setup logic.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::OnceLock;

use dagger_sdk_completeness::extract::policy::{PolicyClauseSelection, extract_policy_clauses};
use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};
use serde_json::json;

const REQUIREMENTS: &str =
    include_str!("../../../../../.kiro/specs/rust-sdk-transport-observability/requirements.md");
const CLIENT_REQUIREMENTS: &str =
    include_str!("../../../../../.kiro/specs/rust-sdk-client-lifecycle/requirements.md");
const CASES: u32 = 256;
const TRANSPORT_VERIFICATION_ID: &str = "verification/feature-3-transport-deterministic";
const EXCLUDED_FEATURE8_ID: &str =
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%2557orkspace";

#[derive(Clone)]
struct ObservationFixture {
    observations: TransportObservationRegistry,
    evidence: EvidenceRegistry,
    target: TargetDescriptor,
    target_digest: TargetDigest,
    ledger: ResolvedLedger,
}

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/transport-scope.txt"
        )))),
        ..Config::default()
    }
}

#[test]
fn reviewed_scope_inventory_and_prior_owners_are_exact() {
    let policy = transport_contract().scope;
    assert_eq!(policy.existing_capability_ids.len(), 32);
    assert_eq!(policy.policy_capability_ids.len(), 26);
    assert_eq!(policy.expected_prior_blocking_owners.len(), 58);
    assert_eq!(
        policy
            .expected_prior_blocking_owners
            .values()
            .filter(|owner| **owner == FeatureId::Feature2)
            .count(),
        11
    );
    assert_eq!(
        policy
            .expected_prior_blocking_owners
            .values()
            .filter(|owner| **owner == FeatureId::Feature3)
            .count(),
        47
    );
    for excluded in [
        "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%254%43oad%2557orkspace%254%44odules",
        "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%2557orkspace",
    ] {
        assert!(
            !policy
                .capability_ids()
                .contains(&CapabilityId::new(excluded).unwrap())
        );
    }
}

#[test]
fn reviewed_transport_descriptor_matches_requirements_source() {
    let policy = transport_contract().scope;
    let section = REQUIREMENTS
        .split_once(policy.existing_scope_heading)
        .unwrap()
        .1;
    let body = section
        .split_once("```text\n")
        .unwrap()
        .1
        .split_once("\n```")
        .unwrap()
        .0;
    let lines = body.lines().map(str::to_owned).collect::<Vec<_>>();
    let ids = CanonicalSet::new(lines.iter().map(|line| CapabilityId::new(line).unwrap()));

    assert_eq!(ids, policy.existing_capability_ids);
    assert_eq!(
        Digest::sha256(serde_json::to_vec(&lines).unwrap()),
        policy.existing_scope_digest
    );
}

#[test]
fn transport_policy_rule_digest_matches_the_reviewed_expansion() {
    let root = repository_root();
    let input: ClassificationInput = decode_canonical(
        &fs::read(root.join("sdk/rust/completeness/classifications.json")).unwrap(),
    )
    .unwrap();
    let rule = input
        .rules
        .get(&RuleId::new("routing/rust-policy/transport-observability").unwrap())
        .unwrap();
    let ExpectedSet::Digest(expected) = &rule.expected_capability_ids else {
        panic!("transport policy expansion must remain digest-fenced");
    };
    assert_eq!(
        canonical_digest(
            DigestDomain::RuleExpansion,
            &transport_contract().scope.policy_capability_ids,
        )
        .unwrap(),
        *expected
    );
}

#[test]
fn rust_policy_source_digest_matches_selected_bytes() {
    let root = repository_root();
    let registry: AuthorityRegistry =
        decode_canonical(&fs::read(root.join("sdk/rust/completeness/authorities.json")).unwrap())
            .unwrap();
    let source = registry
        .authorities
        .get(&AuthorityId::new("rust-policy").unwrap())
        .unwrap();
    let files = source.include.iter().map(|selector| {
        let path = match selector {
            SourceSelector::Path(path) => &path.path,
            SourceSelector::Symbol(symbol) => &symbol.path,
        };
        (path.clone(), fs::read(root.join(path.as_str())).unwrap())
    });
    let observed = recompute_source_digest(source, &SourceBundle::new(files)).unwrap();
    assert_eq!(observed, source.source_digest);
}

#[test]
fn client_lifecycle_descriptor_retains_its_golden_identity() {
    let contract = client_lifecycle_contract();
    let declaration = parse_feature_scope_declaration(CLIENT_REQUIREMENTS, &contract.scope)
        .expect("reviewed client lifecycle declaration");
    let projection = json!({
        "requirements_path": contract.requirements_path,
        "existing_scope_heading": contract.scope.existing_scope_heading,
        "policy_scope_heading": contract.scope.policy_scope_heading,
        "feature": contract.scope.feature,
        "existing_capability_ids": declaration.existing_capability_ids,
        "existing_scope_digest": declaration.existing_scope_digest,
        "policy_capability_ids": declaration.policy_capability_ids,
        "expected_prior_blocking_owners": contract.scope.expected_prior_blocking_owners,
        "policy_clauses": contract.policy_clauses.iter().map(|clause| json!({
            "clause_id": clause.clause_id,
            "exact_text": clause.exact_text,
            "guidance_id": clause.guidance_id,
        })).collect::<Vec<_>>(),
    });
    assert_eq!(
        canonical_digest(DigestDomain::Artifact, &projection).unwrap(),
        Digest::new("sha256:78b486636cc6ddfb1b1d4d718e0fab94ab6fd6a662ec31a31545600f94c63fb5")
            .unwrap()
    );
}

#[test]
fn deterministic_transport_observations_are_canonical_non_live_and_complete() {
    let root = repository_root();
    let bytes =
        fs::read(root.join("sdk/rust/completeness/evidence/transport-observations.json")).unwrap();
    let registry: TransportObservationRegistry = decode_canonical(&bytes).unwrap();
    let record = registry
        .observations
        .get(&EvidenceId::new("verification/feature-3-transport-deterministic").unwrap())
        .unwrap();
    assert_eq!(record.mode, TransportObservationMode::DeterministicFixture);
    assert!(record.exact_target_run.is_none());
    assert_eq!(
        record
            .assertions
            .iter()
            .map(|assertion| assertion.kind.clone())
            .collect::<BTreeSet<_>>(),
        BTreeSet::from([
            TransportObservationKind::Source,
            TransportObservationKind::Acquisition,
            TransportObservationKind::Cache,
            TransportObservationKind::Launch,
            TransportObservationKind::Protocol,
            TransportObservationKind::Http,
            TransportObservationKind::Propagation,
            TransportObservationKind::Compatibility,
            TransportObservationKind::ErrorMapping,
            TransportObservationKind::Shutdown,
        ])
    );
    let observed = CanonicalSet::new(
        record
            .assertions
            .iter()
            .flat_map(|assertion| assertion.capability_ids.iter().cloned()),
    );
    assert_eq!(observed, transport_contract().scope.capability_ids());
    let rendered = String::from_utf8(bytes).unwrap();
    assert!(!rendered.contains("/Users/"));
    assert!(!rendered.contains("localhost:"));
    assert!(!rendered.contains("timestamp"));
    assert!(!rendered.contains("credential"));
}

proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_01_exact_feature_scope_extraction(
        mutation in 0_u8..10,
        noise in any::<u64>(),
    ) {
        let contract = transport_contract();
        let markdown = mutate_transport_requirements(REQUIREMENTS, &contract, mutation, noise);
        let declaration = parse_feature_scope_declaration(&markdown, &contract.scope);
        let authority = AuthorityId::new("rust-policy").unwrap();
        let selections = contract
            .policy_clauses
            .iter()
            .map(|clause| PolicyClauseSelection {
                clause_id: clause.clause_id.to_owned(),
                exact_text: clause.exact_text.to_owned(),
            })
            .collect::<Vec<_>>();
        let clauses = extract_policy_clauses(
            &authority,
            contract.requirements_path,
            &markdown,
            &selections,
        );

        let accepted = declaration.is_ok() && clauses.is_ok();
        prop_assert_eq!(
            accepted,
            mutation == 0,
            "declaration={:?}, clauses={:?}",
            declaration,
            clauses,
        );
    }

    #[test]
    fn property_02_evidence_closed_owner_correct_transitions(
        capability_index in 0_usize..58,
        mutation in 0_u8..12,
    ) {
        let contract = transport_contract();
        let policy = contract.scope;
        let declaration = declaration_for(&policy);
        let target = TargetDigest::new(Digest::sha256("transport target"));
        let selected = policy.capability_ids()[capability_index].clone();
        let outsider = CapabilityId::new(
            "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%2557orkspace",
        )
        .unwrap();
        let mut current = current_ledger(&policy, &outsider);
        let mut candidate = CandidateStatusChanges {
            changes: BTreeMap::from([(selected.clone(), implemented_values())]),
        };
        let mut evidence = complete_evidence(&selected, &target);
        let mut blockers = BTreeMap::new();

        match mutation {
            1 => {
                current.capabilities.get_mut(&selected).unwrap().owner_feature =
                    Some(FeatureId::Feature8);
            }
            2 => {
                candidate.changes.get_mut(&selected).unwrap().implementation_evidence =
                    CanonicalSet::default();
            }
            3 => {
                candidate.changes.get_mut(&selected).unwrap().verification_evidence =
                    CanonicalSet::default();
            }
            4 => {
                evidence
                    .evidence
                    .get_mut(&EvidenceId::new("implementation/transport").unwrap())
                    .unwrap()
                    .execution_target = Some(TargetDigest::new(Digest::sha256("other target")));
            }
            5 => {
                evidence
                    .evidence
                    .get_mut(&EvidenceId::new("verification/transport").unwrap())
                    .unwrap()
                    .proved_capability_ids = CanonicalSet::new([outsider.clone()]);
            }
            6 => {
                evidence
                    .evidence
                    .get_mut(&EvidenceId::new("verification/transport").unwrap())
                    .unwrap()
                    .repository = RepositoryId::new("github.com/dagger/sdk-sdk").unwrap();
            }
            7 => {
                let values = candidate.changes.remove(&selected).unwrap();
                candidate.changes.insert(outsider.clone(), values);
            }
            8 => {
                blockers.insert(
                    selected.clone(),
                    ResidualBlocker {
                        sibling_feature: FeatureId::Feature8,
                        gap: NonEmptyText::new("Live platform verification remains absent").unwrap(),
                    },
                );
            }
            9 => {
                candidate.changes.get_mut(&selected).unwrap().owner_feature =
                    Some(FeatureId::Feature3);
            }
            10 => {
                evidence
                    .evidence
                    .get_mut(&EvidenceId::new("implementation/transport").unwrap())
                    .unwrap()
                    .evidence_kind = EvidenceKind::Verification;
            }
            11 => {
                evidence
                    .evidence
                    .get_mut(&EvidenceId::new("verification/transport").unwrap())
                    .unwrap()
                    .command = None;
            }
            _ => {}
        }

        let result = validate_feature_status_changes(
            &current,
            &declaration,
            &policy,
            &candidate,
            &evidence,
            &target,
            &blockers,
            false,
        );
        prop_assert_eq!(result.is_ok(), mutation == 0);
    }

    // Invariant: fixture observations cannot become live evidence, and an exact-target
    // status transition remains impossible until the complete live lifecycle is proved.
    // Feature: rust-sdk-transport-observability, Property 27: evidence declares what it actually observes
    #[test]
    fn property_27_evidence_declares_actual_observations(
        mutation in 0_u8..12,
        capability_index in 0_usize..58,
    ) {
        let fixture = observation_fixture().clone();
        let mut observations = fixture.observations;
        let mut evidence = fixture.evidence;
        let target = fixture.target;
        let target_digest = fixture.target_digest;
        let mut ledger = fixture.ledger;
        let scope = transport_contract().scope.capability_ids();
        let selected = scope[capability_index].clone();
        let verification_id = EvidenceId::new(TRANSPORT_VERIFICATION_ID).unwrap();
        let excluded_id = CapabilityId::new(EXCLUDED_FEATURE8_ID).unwrap();
        let excluded_before = canonical_bytes(ledger.capabilities.get(&excluded_id).unwrap()).unwrap();
        let mut unknown_shape_rejected = false;

        match mutation {
            1 => {
                let record = observations.observations.get_mut(&verification_id).unwrap();
                record.exact_target_run = Some(exact_run_fixture(&target));
            }
            2 => {
                observations.observations.get_mut(&verification_id).unwrap().mode =
                    TransportObservationMode::ExactTargetLive;
            }
            3 => {
                observations.observations.get_mut(&verification_id).unwrap().target =
                    TargetDigest::new(Digest::sha256("drifted target"));
            }
            4 => {
                let record = observations.observations.get_mut(&verification_id).unwrap();
                record.assertions.retain(|assertion| !assertion.capability_ids.contains(&selected));
            }
            5 => {
                let outsider = CapabilityId::new("policy/rust-policy/transport-unreviewed").unwrap();
                observations.observations.get_mut(&verification_id).unwrap().assertions[0]
                    .capability_ids = CanonicalSet::new([outsider]);
            }
            6 => {
                let reference = evidence.evidence.get_mut(&verification_id).unwrap();
                reference.proved_capability_ids = CanonicalSet::new(
                    reference.proved_capability_ids.iter().filter(|id| **id != selected).cloned(),
                );
            }
            7 => {
                ledger.capabilities.get_mut(&selected).unwrap().status = Status::Implemented;
            }
            8 => {
                evidence.evidence.get_mut(&verification_id).unwrap()
                    .expected_outcome.as_mut().unwrap().outcome = CheckOutcome::Failed;
            }
            9 => {
                observations.observations.get_mut(&verification_id).unwrap().platform_scope =
                    CanonicalSet::new([Platform {
                        operating_system: OperatingSystem::Macos,
                        architecture: Architecture::Arm64,
                    }]);
            }
            10 => {
                let mut value = serde_json::to_value(&observations).unwrap();
                value["observations"][verification_id.as_str()]["unknown_live_claim"] = json!(true);
                unknown_shape_rejected = serde_json::from_value::<TransportObservationRegistry>(value).is_err();
            }
            11 => {
                evidence.evidence.remove(&verification_id);
            }
            _ => {}
        }

        let accepted = if mutation == 10 {
            !unknown_shape_rejected
        } else {
            validate_transport_observations(
                &observations,
                &evidence,
                &target,
                &target_digest,
                &scope,
                &ledger,
            ).is_ok()
        };
        prop_assert_eq!(accepted, mutation == 0);
        prop_assert_eq!(
            canonical_bytes(ledger.capabilities.get(&excluded_id).unwrap()).unwrap(),
            excluded_before,
        );
    }
}

fn exact_run_fixture(target: &TargetDescriptor) -> ExactTargetRun {
    ExactTargetRun {
        rust_sdk_version: target.rust_sdk_version.clone(),
        cli_version: target.engine_version.clone(),
        observed_engine_version: DaggerVersion::new(format!(
            "{}+{}",
            target.engine_version,
            &target.dagger_revision.as_str()[..8],
        ))
        .unwrap(),
        dagger_revision: target.dagger_revision.clone(),
        sdk_started_session: true,
        authenticated_query: true,
        propagation_observed: true,
        diagnostic_boundary_observed: true,
        explicit_close: true,
        child_reaped: true,
    }
}

fn observation_fixture() -> &'static ObservationFixture {
    static FIXTURE: OnceLock<ObservationFixture> = OnceLock::new();
    FIXTURE.get_or_init(|| {
        let root = repository_root();
        let observations = decode_canonical(
            &fs::read(root.join("sdk/rust/completeness/evidence/transport-observations.json"))
                .unwrap(),
        )
        .unwrap();
        let mut evidence: EvidenceRegistry = decode_canonical(
            &fs::read(root.join("sdk/rust/completeness/evidence/registry.json")).unwrap(),
        )
        .unwrap();
        let verification_id = EvidenceId::new(TRANSPORT_VERIFICATION_ID).unwrap();
        evidence
            .evidence
            .retain(|evidence_id, _| evidence_id == &verification_id);
        let target: TargetDescriptor =
            decode_canonical(&fs::read(root.join("sdk/rust/completeness/target.json")).unwrap())
                .unwrap();
        let target_digest =
            TargetDigest::new(canonical_digest(DigestDomain::Target, &target).unwrap());
        let mut ledger: ResolvedLedger = decode_canonical(
            &fs::read(root.join("sdk/rust/completeness/artifacts/ledger.json")).unwrap(),
        )
        .unwrap();
        let scope = transport_contract().scope.capability_ids();
        let excluded_id = CapabilityId::new(EXCLUDED_FEATURE8_ID).unwrap();
        ledger.capabilities.retain(|capability_id, _| {
            scope.contains(capability_id) || capability_id == &excluded_id
        });
        ObservationFixture {
            observations,
            evidence,
            target,
            target_digest,
            ledger,
        }
    })
}

fn mutate_transport_requirements(
    markdown: &str,
    contract: &FeatureContractPolicy,
    mutation: u8,
    noise: u64,
) -> String {
    let first = contract
        .scope
        .existing_capability_ids
        .first()
        .unwrap()
        .as_str();
    let second = contract.scope.existing_capability_ids[1].as_str();
    match mutation {
        1 => markdown.replacen(first, &format!("{first}\n{first}"), 1),
        2 => markdown.replacen(
            &format!("{first}\n{second}"),
            &format!("{second}\n{first}"),
            1,
        ),
        3 => markdown.replacen(&format!("{first}\n"), "", 1),
        4 => markdown.replacen(
            first,
            &format!("{first}\nbehavior/go-client/unreviewed-{noise}"),
            1,
        ),
        5 => markdown.replacen(first, "Behavior/not-canonical", 1),
        6 => markdown.replacen(
            contract.scope.existing_scope_digest.as_str(),
            "sha256:1b4246157f75b8ce179d8fec3476256fa939ccdf69d29d1fcafaf93f160013b3",
            1,
        ),
        7 => {
            let last = contract
                .scope
                .policy_capability_ids
                .last()
                .unwrap()
                .as_str();
            markdown.replacen(last, "policy/rust-policy/transport-unreviewed", 1)
        }
        8 => markdown.replacen(
            contract.policy_clauses[0].exact_text,
            "Every owned process failure may be ignored after startup.",
            1,
        ),
        9 => markdown.replacen(
            contract.scope.existing_scope_heading,
            &format!(
                "{}\n{}",
                contract.scope.existing_scope_heading, contract.scope.existing_scope_heading
            ),
            1,
        ),
        _ => markdown.to_owned(),
    }
}

fn declaration_for(policy: &FeatureScopePolicy) -> FeatureScopeDeclaration {
    FeatureScopeDeclaration {
        feature: policy.feature.clone(),
        existing_capability_ids: policy.existing_capability_ids.clone(),
        existing_scope_digest: policy.existing_scope_digest.clone(),
        policy_capability_ids: policy.policy_capability_ids.clone(),
    }
}

fn current_ledger(policy: &FeatureScopePolicy, outsider: &CapabilityId) -> ResolvedLedger {
    let mut capabilities = policy
        .capability_ids()
        .iter()
        .map(|capability_id| {
            let owner = policy
                .expected_prior_blocking_owners
                .get(capability_id)
                .unwrap()
                .clone();
            (
                capability_id.clone(),
                blocking_record(capability_id, owner, "Transport remains unverified"),
            )
        })
        .collect::<BTreeMap<_, _>>();
    capabilities.insert(
        outsider.clone(),
        blocking_record(
            outsider,
            FeatureId::Feature2,
            "Live platform verification remains absent",
        ),
    );
    ResolvedLedger { capabilities }
}

fn blocking_record(capability_id: &CapabilityId, owner: FeatureId, gap: &str) -> CapabilityRecord {
    let authority = capability_id.as_str().split('/').nth(1).unwrap();
    let source_item_id = SourceItemId::new(format!("source/{authority}/fixture")).unwrap();
    CapabilityRecord {
        capability_id: capability_id.clone(),
        authority_id: AuthorityId::new(authority).unwrap(),
        capability_kind: CapabilityKind::new("fixture").unwrap(),
        source_item_ids: CanonicalSet::new([source_item_id]),
        source_anchors: CanonicalSet::new([authority_evidence(capability_id)]),
        summary: NonEmptyText::new("Transport fixture").unwrap(),
        semantic_signature: json!({"capability": capability_id}),
        capability_fingerprint: Digest::sha256(capability_id.as_str()),
        status: Status::Missing,
        stability: Stability::Stable,
        gap: Some(NonEmptyText::new(gap).unwrap()),
        owner_feature: Some(owner),
        implementation_evidence: CanonicalSet::default(),
        verification_evidence: CanonicalSet::default(),
        decision_evidence: CanonicalSet::default(),
    }
}

fn authority_evidence(capability_id: &CapabilityId) -> EvidenceReference {
    EvidenceReference {
        evidence_id: EvidenceId::new(format!("authority/{capability_id}")).unwrap(),
        evidence_kind: EvidenceKind::Authority,
        repository: RepositoryId::new("github.com/dagger/dagger").unwrap(),
        revision: CommitSha::new("a".repeat(40)).unwrap(),
        path: RepositoryRelativePath::new("sdk/rust/completeness/fixture.json").unwrap(),
        locator: SourceLocator::new("sdk/rust/completeness/fixture.json#authority").unwrap(),
        claim: NonEmptyText::new("Defines the fixture capability").unwrap(),
        command: None,
        expected_outcome: None,
        execution_target: None,
        platform_scope: CanonicalSet::default(),
        proved_capability_ids: CanonicalSet::new([capability_id.clone()]),
    }
}

fn implemented_values() -> ClassificationValues {
    ClassificationValues {
        status: Status::Implemented,
        gap: None,
        owner_feature: None,
        implementation_evidence: CanonicalSet::new([
            EvidenceId::new("implementation/transport").unwrap()
        ]),
        verification_evidence: CanonicalSet::new([
            EvidenceId::new("verification/transport").unwrap()
        ]),
        decision_evidence: CanonicalSet::default(),
    }
}

fn complete_evidence(capability_id: &CapabilityId, target: &TargetDigest) -> EvidenceRegistry {
    let implementation = candidate_evidence(
        "implementation/transport",
        EvidenceKind::Implementation,
        capability_id,
        target,
        false,
    );
    let verification = candidate_evidence(
        "verification/transport",
        EvidenceKind::Verification,
        capability_id,
        target,
        true,
    );
    EvidenceRegistry {
        evidence: BTreeMap::from([
            (implementation.evidence_id.clone(), implementation),
            (verification.evidence_id.clone(), verification),
        ]),
    }
}

fn candidate_evidence(
    id: &str,
    kind: EvidenceKind,
    capability_id: &CapabilityId,
    target: &TargetDigest,
    executable: bool,
) -> EvidenceReference {
    EvidenceReference {
        evidence_id: EvidenceId::new(id).unwrap(),
        evidence_kind: kind,
        repository: RepositoryId::new("github.com/dagger/dagger").unwrap(),
        revision: CommitSha::new("b".repeat(40)).unwrap(),
        path: RepositoryRelativePath::new(if executable {
            "sdk/rust/crates/dagger-sdk/tests/transport.rs"
        } else {
            "sdk/rust/crates/dagger-sdk/src/transport.rs"
        })
        .unwrap(),
        locator: SourceLocator::new(if executable {
            "sdk/rust/crates/dagger-sdk/tests/transport.rs#property"
        } else {
            "sdk/rust/crates/dagger-sdk/src/transport.rs#implementation"
        })
        .unwrap(),
        claim: NonEmptyText::new("Verifies the routed transport capability").unwrap(),
        command: executable.then(|| CommandSpec {
            program: ExecutableId::new("cargo").unwrap(),
            args: vec!["test".to_owned(), "--locked".to_owned()],
            working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
            environment: BTreeMap::new(),
        }),
        expected_outcome: executable.then(|| ExpectedOutcome {
            outcome: CheckOutcome::Passed,
            assertion: NonEmptyText::new("Transport property passes").unwrap(),
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

fn repository_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .ancestors()
        .nth(4)
        .unwrap()
        .to_path_buf()
}
