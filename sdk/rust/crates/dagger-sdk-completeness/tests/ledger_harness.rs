//! Executable properties for ledger truthfulness and bounded harness evidence.
//!
//! Each generator starts from one coherent contract fragment and applies one named boundary
//! mutation. This keeps the 256-case properties focused on the reviewed policy tables rather than
//! on malformed scaffolding unrelated to the invariant under test.

use std::collections::{BTreeMap, BTreeSet};
use std::path::Path;

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const CASES: u32 = 256;

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/ledger-harness.txt"
        )))),
        ..Config::default()
    }
}

#[derive(Clone)]
struct Fixture {
    authority_id: AuthorityId,
    repository: RepositoryId,
    revision: CommitSha,
    target: TargetDigest,
    platform: Platform,
    source_items: SourceItemInventory,
    inventory: CanonicalInventory,
    definition: CapabilityDefinition,
    authority_anchor: EvidenceReference,
    implementation: EvidenceReference,
    verification: EvidenceReference,
    decision: EvidenceReference,
    command_policy: CommandPolicy,
}

fn fixture() -> Fixture {
    let authority_id = AuthorityId::new("go-client").unwrap();
    let repository = RepositoryId::new("github.com/dagger/dagger").unwrap();
    let revision = CommitSha::new("a".repeat(40)).unwrap();
    let target = TargetDigest::new(Digest::sha256("target"));
    let platform = Platform {
        operating_system: OperatingSystem::Linux,
        architecture: Architecture::Amd64,
    };
    let capability_id = CapabilityId::new("behavior/go-client/connect").unwrap();
    let source_item_id = SourceItemId::new("source/go-client/connect").unwrap();
    let source_locator = SourceLocator::new("client.go#Connect").unwrap();
    let semantic_signature = serde_json::json!({"behavior": "connect", "observable": true});
    let fingerprint = canonical_digest(DigestDomain::Capability, &semantic_signature).unwrap();
    let authority_anchor = evidence_reference(
        "authority/connect",
        EvidenceKind::Authority,
        &repository,
        &revision,
        "sdk/go/client.go",
        "client.go#Connect",
        "defines client connection behavior",
        &capability_id,
        None,
        None,
        None,
        CanonicalSet::default(),
    );
    let definition = CapabilityDefinition {
        capability_id: capability_id.clone(),
        authority_id: authority_id.clone(),
        capability_kind: CapabilityKind::new("behavior/client-lifecycle").unwrap(),
        source_item_ids: CanonicalSet::new([source_item_id.clone()]),
        source_anchors: CanonicalSet::new([authority_anchor.clone()]),
        summary: NonEmptyText::new("Connect a client to the selected engine").unwrap(),
        semantic_signature: semantic_signature.clone(),
        capability_fingerprint: fingerprint.clone(),
        stability: Stability::Stable,
    };
    let source_items = SourceItemInventory {
        items: BTreeMap::from([(
            source_item_id.clone(),
            SourceItem {
                source_item_id,
                authority_id: authority_id.clone(),
                item_kind: SourceItemKind::new("go-exported-function").unwrap(),
                locator: source_locator,
                semantic_signature,
                fingerprint,
                state: SourceItemState::Active,
            },
        )]),
    };
    let inventory = CanonicalInventory {
        capabilities: BTreeMap::from([(capability_id.clone(), definition.clone())]),
    };
    let command = CommandSpec {
        program: ExecutableId::new("cargo").unwrap(),
        args: vec!["test".to_owned(), "--locked".to_owned()],
        working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
        environment: BTreeMap::from([("RUST_BACKTRACE".to_owned(), "1".to_owned())]),
    };
    let expected = ExpectedOutcome {
        outcome: CheckOutcome::Passed,
        assertion: NonEmptyText::new("connects to selected engine").unwrap(),
    };
    let implementation = evidence_reference(
        "implementation/connect",
        EvidenceKind::Implementation,
        &repository,
        &revision,
        "sdk/rust/crates/dagger-sdk/src/core/client.rs",
        "client.rs#connect",
        "implements client connection behavior",
        &capability_id,
        None,
        None,
        Some(target.clone()),
        CanonicalSet::default(),
    );
    let verification = evidence_reference(
        "verification/connect",
        EvidenceKind::Verification,
        &repository,
        &revision,
        "sdk/rust/crates/dagger-sdk/tests/connect.rs",
        "connect.rs#connects_to_selected_engine",
        "verifies client connection behavior",
        &capability_id,
        Some(command),
        Some(expected),
        Some(target.clone()),
        CanonicalSet::new([platform.clone()]),
    );
    let decision = evidence_reference(
        "decision/connect",
        EvidenceKind::Decision,
        &repository,
        &revision,
        ".kiro/specs/rust-sdk-client/design.md",
        "design.md#connection-shape",
        "no meaningful Rust counterpart exists for this language-specific mechanism",
        &capability_id,
        None,
        None,
        Some(target.clone()),
        CanonicalSet::default(),
    );
    let command_policy = CommandPolicy {
        programs: BTreeSet::from([ExecutableId::new("cargo").unwrap()]),
        working_directories: BTreeSet::from([RepositoryRelativePath::new("sdk/rust").unwrap()]),
        environment_keys: BTreeSet::from(["RUST_BACKTRACE".to_owned()]),
    };

    Fixture {
        authority_id,
        repository,
        revision,
        target,
        platform,
        source_items,
        inventory,
        definition,
        authority_anchor,
        implementation,
        verification,
        decision,
        command_policy,
    }
}

#[allow(clippy::too_many_arguments)]
fn evidence_reference(
    evidence_id: &str,
    evidence_kind: EvidenceKind,
    repository: &RepositoryId,
    revision: &CommitSha,
    path: &str,
    locator: &str,
    claim: &str,
    capability_id: &CapabilityId,
    command: Option<CommandSpec>,
    expected_outcome: Option<ExpectedOutcome>,
    execution_target: Option<TargetDigest>,
    platform_scope: CanonicalSet<Platform>,
) -> EvidenceReference {
    EvidenceReference {
        evidence_id: EvidenceId::new(evidence_id).unwrap(),
        evidence_kind,
        repository: repository.clone(),
        revision: revision.clone(),
        path: RepositoryRelativePath::new(path).unwrap(),
        locator: SourceLocator::new(locator).unwrap(),
        claim: NonEmptyText::new(claim).unwrap(),
        command,
        expected_outcome,
        execution_target,
        platform_scope,
        proved_capability_ids: CanonicalSet::new([capability_id.clone()]),
    }
}

fn values_for(status: Status, fixture: &Fixture) -> ClassificationValues {
    let blocking = matches!(status, Status::Missing | Status::Partial);
    ClassificationValues {
        status: status.clone(),
        gap: blocking.then(|| NonEmptyText::new("observable behavior remains incomplete").unwrap()),
        owner_feature: blocking.then_some(FeatureId::Feature2),
        implementation_evidence: if matches!(
            status,
            Status::Partial | Status::Implemented | Status::IdiomaticEquivalent
        ) {
            CanonicalSet::new([fixture.implementation.evidence_id.clone()])
        } else {
            CanonicalSet::default()
        },
        verification_evidence: if matches!(
            status,
            Status::Implemented | Status::IdiomaticEquivalent
        ) {
            CanonicalSet::new([fixture.verification.evidence_id.clone()])
        } else {
            CanonicalSet::default()
        },
        decision_evidence: if matches!(status, Status::IdiomaticEquivalent | Status::Inapplicable) {
            CanonicalSet::new([fixture.decision.evidence_id.clone()])
        } else {
            CanonicalSet::default()
        },
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

fn registry(fixture: &Fixture) -> EvidenceRegistry {
    EvidenceRegistry {
        evidence: BTreeMap::from([
            (
                fixture.implementation.evidence_id.clone(),
                fixture.implementation.clone(),
            ),
            (
                fixture.verification.evidence_id.clone(),
                fixture.verification.clone(),
            ),
            (
                fixture.decision.evidence_id.clone(),
                fixture.decision.clone(),
            ),
        ]),
    }
}

fn alternate_target() -> TargetDigest {
    TargetDigest::new(Digest::sha256("alternate-target"))
}

// Invariant: rule expansion is identical to the simple attribute filter and exact-set fence.
// Feature: rust-sdk-completeness-contract, Property 7: exact classification-rule expansion
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_7_exact_classification_rule_expansion(
        count in 1_usize..12,
        use_digest in any::<bool>(),
        mutation in 0_u8..8,
    ) {
        let base = fixture();
        let mut inventory = CanonicalInventory::default();
        let mut source_items = SourceItemInventory::default();
        for index in 0..count {
            let capability_id = CapabilityId::new(format!("behavior/go-client/connect/{index}"))
                .unwrap();
            let source_id = SourceItemId::new(format!("source/go-client/connect/{index}"))
                .unwrap();
            let mut definition = base.definition.clone();
            definition.capability_id = capability_id.clone();
            definition.source_item_ids = CanonicalSet::new([source_id.clone()]);
            definition.source_anchors = CanonicalSet::new([{
                let mut anchor = base.authority_anchor.clone();
                anchor.evidence_id = EvidenceId::new(format!("authority/connect/{index}")).unwrap();
                anchor.proved_capability_ids = CanonicalSet::new([capability_id.clone()]);
                anchor
            }]);
            let mut source = base.source_items.items.values().next().unwrap().clone();
            source.source_item_id = source_id.clone();
            source_items.items.insert(source_id, source);
            inventory.capabilities.insert(capability_id, definition);
        }
        let expected_ids = CanonicalSet::new(inventory.capabilities.keys().cloned());
        let expected = if use_digest {
            ExpectedSet::Digest(canonical_digest(DigestDomain::RuleExpansion, &expected_ids).unwrap())
        } else {
            ExpectedSet::CapabilityIds(expected_ids.clone())
        };
        let rule_id = RuleId::new("all-connect").unwrap();
        let mut rule = ClassificationRule {
            rule_id: rule_id.clone(),
            authority_id: base.authority_id.clone(),
            selector: ClassificationSelector {
                authority_id: Some(base.authority_id.clone()),
                capability_kind: Some(base.definition.capability_kind.clone()),
                stability: Some(Stability::Stable),
                source_item_kind: Some(SourceItemKind::new("go-exported-function").unwrap()),
                capability_id_prefix: Some(CapabilityId::new("behavior/go-client/connect").unwrap()),
            },
            expected_capability_ids: expected,
            classification: values_for(Status::Missing, &base),
            overrides: BTreeMap::new(),
        };
        let mut input = ClassificationInput {
            exact: BTreeMap::new(),
            rules: BTreeMap::from([(rule_id.clone(), rule.clone())]),
        };
        match mutation {
            1 => {
                let mut definition = base.definition.clone();
                definition.capability_id = CapabilityId::new("behavior/go-client/connect/new").unwrap();
                definition.source_item_ids = CanonicalSet::default();
                inventory.capabilities.insert(definition.capability_id.clone(), definition);
            }
            2 => {
                inventory.capabilities.pop_last();
            }
            3 => {
                let first = inventory.capabilities.keys().next().unwrap().clone();
                input.exact.insert(first, values_for(Status::Missing, &base));
            }
            4 => {
                rule.overrides.insert(
                    CapabilityId::new("behavior/go-client/connect/stale").unwrap(),
                    values_for(Status::Partial, &base),
                );
                input.rules.insert(rule_id.clone(), rule);
            }
            5 => input.rules.clear(),
            6 => {
                rule.expected_capability_ids = ExpectedSet::Digest(Digest::sha256("wrong"));
                input.rules.insert(rule_id.clone(), rule);
            }
            7 => {
                rule.selector.authority_id = Some(AuthorityId::new("other-authority").unwrap());
                input.rules.insert(rule_id, rule);
            }
            _ => {}
        }

        let result = resolve_classifications(&inventory, &source_items, &input);
        prop_assert_eq!(result.is_ok(), mutation == 0);
        if mutation == 0 {
            let ledger = result.unwrap();
            let resolved = CanonicalSet::new(ledger.capabilities.keys().cloned());
            prop_assert_eq!(resolved, expected_ids);
        }
    }
}

// Invariant: each of the five statuses accepts only its reviewed gap/owner/evidence shape.
// Feature: rust-sdk-completeness-contract, Property 8: status-entry state machine
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_8_status_entry_state_machine(status_index in 0_u8..5, mutation in 0_u8..7) {
        let fixture = fixture();
        let status = [
            Status::Missing,
            Status::Partial,
            Status::Implemented,
            Status::IdiomaticEquivalent,
            Status::Inapplicable,
        ][usize::from(status_index)]
            .clone();
        let mut values = values_for(status.clone(), &fixture);
        let mut definition = fixture.definition.clone();
        let mut evidence = registry(&fixture);
        match mutation {
            1 => {
                if matches!(status, Status::Missing | Status::Partial) {
                    values.gap = None;
                } else {
                    values.gap = Some(NonEmptyText::new("stale gap").unwrap());
                }
            }
            2 => {
                if matches!(status, Status::Missing | Status::Partial) {
                    values.owner_feature = None;
                } else {
                    values.owner_feature = Some(FeatureId::Feature2);
                }
            }
            3 => {
                if matches!(status, Status::Missing | Status::Inapplicable) {
                    values.implementation_evidence =
                        CanonicalSet::new([fixture.implementation.evidence_id.clone()]);
                } else {
                    values.implementation_evidence = CanonicalSet::default();
                }
            }
            4 => {
                if matches!(status, Status::Implemented | Status::IdiomaticEquivalent) {
                    values.verification_evidence = CanonicalSet::default();
                } else {
                    values.verification_evidence =
                        CanonicalSet::new([fixture.verification.evidence_id.clone()]);
                    if status == Status::Partial {
                        values.implementation_evidence = CanonicalSet::default();
                    }
                }
            }
            5 => definition.source_anchors = CanonicalSet::default(),
            6 => {
                values = values_for(Status::Implemented, &fixture);
                evidence
                    .evidence
                    .get_mut(&fixture.verification.evidence_id)
                    .unwrap()
                    .path = RepositoryRelativePath::new("sdk/rust/README.md").unwrap();
            }
            _ => {}
        }
        let row = record(&definition, &values);
        let ledger = ResolvedLedger {
            capabilities: BTreeMap::from([(row.capability_id.clone(), row)]),
        };

        let result = validate_status_entries(&ledger, &evidence);
        prop_assert_eq!(result.is_ok(), mutation == 0);
    }
}

fn authority_registry(fixture: &Fixture) -> AuthorityRegistry {
    AuthorityRegistry {
        authorities: BTreeMap::from([(
            fixture.authority_id.clone(),
            AuthoritySource {
                authority_id: fixture.authority_id.clone(),
                authority_class: AuthorityClass::GoClient,
                repository: fixture.repository.clone(),
                revision: fixture.revision.clone(),
                include: CanonicalSet::new([SourceSelector::Path(PathSourceSelector {
                    path: RepositoryRelativePath::new("sdk").unwrap(),
                })]),
                exclude: CanonicalSet::default(),
                extractor: ExtractorIdentity {
                    extractor_id: ExtractorId::new("go-source").unwrap(),
                    version: SemverVersion::new("1.0.0").unwrap(),
                },
                source_digest: Digest::sha256("source"),
            },
        )]),
    }
}

fn evidence_sources(fixture: &Fixture) -> EvidenceSourceRegistry {
    EvidenceSourceRegistry::new([
        EvidenceSource {
            repository: fixture.authority_anchor.repository.clone(),
            revision: fixture.authority_anchor.revision.clone(),
            path: fixture.authority_anchor.path.clone(),
            locator: fixture.authority_anchor.locator.clone(),
            state: SourceItemState::Active,
            eligibility: EvidenceEligibility::SourceOnly,
        },
        EvidenceSource {
            repository: fixture.implementation.repository.clone(),
            revision: fixture.implementation.revision.clone(),
            path: fixture.implementation.path.clone(),
            locator: fixture.implementation.locator.clone(),
            state: SourceItemState::Active,
            eligibility: EvidenceEligibility::SourceOnly,
        },
        EvidenceSource {
            repository: fixture.verification.repository.clone(),
            revision: fixture.verification.revision.clone(),
            path: fixture.verification.path.clone(),
            locator: fixture.verification.locator.clone(),
            state: SourceItemState::Active,
            eligibility: EvidenceEligibility::ExecutableAssertion,
        },
        EvidenceSource {
            repository: fixture.decision.repository.clone(),
            revision: fixture.decision.revision.clone(),
            path: fixture.decision.path.clone(),
            locator: fixture.decision.locator.clone(),
            state: SourceItemState::Active,
            eligibility: EvidenceEligibility::Documentation,
        },
    ])
}

// Invariant: executable evidence is accepted only at an exact pinned locator and reverse scope.
// Feature: rust-sdk-completeness-contract, Property 9: evidence provenance and scope
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_9_evidence_provenance_and_scope(mutation in 0_u8..21) {
        let fixture = fixture();
        let values = values_for(Status::Implemented, &fixture);
        let mut row = record(&fixture.definition, &values);
        let mut evidence = registry(&fixture);
        evidence.evidence.remove(&fixture.decision.evidence_id);
        let verification = evidence
            .evidence
            .get_mut(&fixture.verification.evidence_id)
            .unwrap();
        let mut sources = evidence_sources(&fixture);
        match mutation {
            1 => verification.repository = RepositoryId::new("github.com/example/other").unwrap(),
            2 => verification.revision = CommitSha::new("b".repeat(40)).unwrap(),
            3 => verification.path = RepositoryRelativePath::new("sdk/rust/tests/missing.rs").unwrap(),
            4 => verification.locator = SourceLocator::new("missing_test").unwrap(),
            5 => verification.command.as_mut().unwrap().program = ExecutableId::new("bash").unwrap(),
            6 => verification.expected_outcome.as_mut().unwrap().outcome = CheckOutcome::Failed,
            7 => verification.execution_target = Some(alternate_target()),
            8 => verification.platform_scope = CanonicalSet::default(),
            9 => verification.proved_capability_ids = CanonicalSet::default(),
            10 => row.verification_evidence = CanonicalSet::default(),
            11 => {
                sources = EvidenceSourceRegistry::new(
                    sources.sources().iter().cloned().map(|mut source| {
                        if source.locator == fixture.verification.locator {
                            source.eligibility = EvidenceEligibility::Documentation;
                        }
                        source
                    }),
                );
            }
            12 => {
                sources = EvidenceSourceRegistry::new(
                    sources.sources().iter().cloned().map(|mut source| {
                        if source.locator == fixture.verification.locator {
                            source.state = SourceItemState::Removed;
                        }
                        source
                    }),
                );
            }
            13 => {
                sources = EvidenceSourceRegistry::new(
                    sources.sources().iter().cloned().map(|mut source| {
                        if source.locator == fixture.verification.locator {
                            source.state = SourceItemState::Skipped;
                        }
                        source
                    }),
                );
            }
            14 => {
                sources = EvidenceSourceRegistry::new(
                    sources.sources().iter().cloned().map(|mut source| {
                        if source.locator == fixture.verification.locator {
                            source.eligibility = EvidenceEligibility::Issue;
                        }
                        source
                    }),
                );
            }
            15 => {
                sources = EvidenceSourceRegistry::new(
                    sources.sources().iter().cloned().map(|mut source| {
                        if source.locator == fixture.verification.locator {
                            source.eligibility = EvidenceEligibility::PullRequest;
                        }
                        source
                    }),
                );
            }
            16 => {
                sources = EvidenceSourceRegistry::new(
                    sources.sources().iter().cloned().map(|mut source| {
                        if source.locator == fixture.verification.locator {
                            source.state = SourceItemState::HarnessSelf;
                            source.eligibility = EvidenceEligibility::HarnessSelf;
                        }
                        source
                    }),
                );
            }
            17 => verification.expected_outcome.as_mut().unwrap().outcome = CheckOutcome::Error,
            18 => {
                sources = EvidenceSourceRegistry::new(
                    sources.sources().iter().cloned().map(|mut source| {
                        if source.locator == fixture.verification.locator {
                            source.eligibility = EvidenceEligibility::SourceOnly;
                        }
                        source
                    }),
                );
            }
            19 => {
                verification.command.as_mut().unwrap().working_directory =
                    RepositoryRelativePath::new("unreviewed/directory").unwrap();
            }
            20 => {
                verification
                    .command
                    .as_mut()
                    .unwrap()
                    .environment
                    .insert("API_TOKEN".to_owned(), "must-not-enter-artifacts".to_owned());
            }
            _ => {}
        }
        let ledger = ResolvedLedger {
            capabilities: BTreeMap::from([(row.capability_id.clone(), row)]),
        };
        let authorities = authority_registry(&fixture);
        let context = EvidenceAuditContext {
            authorities: &authorities,
            sources: &sources,
            inventory: &fixture.inventory,
            target: &fixture.target,
            command_policy: &fixture.command_policy,
        };

        let result = audit_evidence_registry(evidence, &ledger, &context);
        prop_assert_eq!(result.is_ok(), mutation == 0);
    }
}

fn all_domains() -> Vec<BlockingDomain> {
    vec![
        BlockingDomain::ClientLifecycle,
        BlockingDomain::TransportObservabilityProvisioningReliability,
        BlockingDomain::CoreSchemaGeneration,
        BlockingDomain::EngineSdkResolutionRuntimeGeneratorIntegration,
        BlockingDomain::ModuleAuthoringTypeDiscoveryDispatch,
        BlockingDomain::StandaloneClientGeneration,
        BlockingDomain::DependencyClientGeneration,
        BlockingDomain::InitClient,
        BlockingDomain::Conformance,
        BlockingDomain::Platform,
        BlockingDomain::Security,
        BlockingDomain::PackagingReleaseCompatibilityDocumentation,
    ]
}

// Invariant: one explicit umbrella domain determines exactly one Feature 2-9 owner.
// Feature: rust-sdk-completeness-contract, Property 17: blocking-work ownership
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_17_blocking_work_ownership(
        domain_index in 0_usize..12,
        partial in any::<bool>(),
        mutation in 0_u8..3,
    ) {
        let fixture = fixture();
        let domain = all_domains()[domain_index].clone();
        let status = if partial { Status::Partial } else { Status::Missing };
        let mut values = values_for(status, &fixture);
        values.owner_feature = Some(domain.feature());
        let row = record(&fixture.definition, &values);
        let ledger = ResolvedLedger {
            capabilities: BTreeMap::from([(row.capability_id.clone(), row)]),
        };
        let mut assignments = OwnershipAssignments {
            assignments: BTreeMap::from([(fixture.definition.capability_id.clone(), domain.clone())]),
        };
        if mutation == 1 {
            assignments.assignments.clear();
        } else if mutation == 2 {
            let replacement = if domain.feature() == FeatureId::Feature2 {
                BlockingDomain::TransportObservabilityProvisioningReliability
            } else {
                BlockingDomain::ClientLifecycle
            };
            assignments
                .assignments
                .insert(fixture.definition.capability_id.clone(), replacement);
        }

        prop_assert_eq!(
            validate_blocking_ownership(&ledger, &assignments).is_ok(),
            mutation == 0
        );
    }
}

// Invariant: child declarations and destination-status evidence travel in one candidate contract.
// Feature: rust-sdk-completeness-contract, Property 18: downstream traceability preservation
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_18_downstream_traceability_preservation(
        destination_index in 0_u8..4,
        mutation in 0_u8..6,
    ) {
        let fixture = fixture();
        let current_values = values_for(Status::Missing, &fixture);
        let current_row = record(&fixture.definition, &current_values);
        let current = ResolvedLedger {
            capabilities: BTreeMap::from([(current_row.capability_id.clone(), current_row)]),
        };
        let destination = [
            Status::Partial,
            Status::Implemented,
            Status::IdiomaticEquivalent,
            Status::Inapplicable,
        ][usize::from(destination_index)]
            .clone();
        let mut replacement = values_for(destination.clone(), &fixture);
        let mut declaration = ChildSpecDeclaration {
            feature: FeatureId::Feature2,
            capability_ids: CanonicalSet::new([fixture.definition.capability_id.clone()]),
        };
        let mut evidence = registry(&fixture);
        match mutation {
            1 => {
                declaration.capability_ids = CanonicalSet::new([
                    CapabilityId::new("behavior/unknown").unwrap(),
                ]);
            }
            2 => declaration.capability_ids = CanonicalSet::default(),
            3 => replacement = current_values,
            4 => match destination {
                Status::Partial => replacement.implementation_evidence = CanonicalSet::default(),
                Status::Implemented => replacement.verification_evidence = CanonicalSet::default(),
                Status::IdiomaticEquivalent | Status::Inapplicable => {
                    replacement.decision_evidence = CanonicalSet::default();
                }
                Status::Missing => unreachable!(),
            },
            5 => evidence.evidence.clear(),
            _ => {}
        }
        let candidate = CandidateStatusChanges {
            changes: BTreeMap::from([(
                fixture.definition.capability_id.clone(),
                replacement,
            )]),
        };

        prop_assert_eq!(
            validate_downstream_traceability(&current, &declaration, &candidate, &evidence).is_ok(),
            mutation == 0
        );
    }
}

#[derive(Clone)]
struct HarnessFixture {
    fixture: Fixture,
    checks: HarnessCheckInventory,
    mappings: HarnessMappings,
    mapping: HarnessCheckMapping,
    result: HarnessCheckResult,
    policy: CommandPolicy,
}

fn harness_fixture() -> HarnessFixture {
    let fixture = fixture();
    let check_id = CheckId::new("module-start").unwrap();
    let revision = CommitSha::new("c".repeat(40)).unwrap();
    let locator = SourceLocator::new("checks/module.dagger#moduleStart").unwrap();
    let fingerprint = Digest::sha256("module-start-source");
    let cli_digest = Digest::sha256("dagger-cli");
    let artifact_digest = Digest::sha256("rust-sdk-workspace");
    let command = CommandSpec {
        program: ExecutableId::new("dagger").unwrap(),
        args: vec![
            "check".to_owned(),
            check_id.to_string(),
            "--no-generate".to_owned(),
        ],
        working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
        environment: BTreeMap::from([("DAGGER_LOG_FORMAT".to_owned(), "plain".to_owned())]),
    };
    let expected = ExpectedOutcome {
        outcome: CheckOutcome::Passed,
        assertion: NonEmptyText::new("module starts and serves its root object").unwrap(),
    };
    let source = HarnessCheckSource {
        check_id: check_id.clone(),
        check_kind: HarnessCheckKind::SubjectConformance,
        harness_revision: revision.clone(),
        source_locator: locator.clone(),
        source_fingerprint: fingerprint.clone(),
    };
    let mapping = HarnessCheckMapping {
        check_id: check_id.clone(),
        check_kind: HarnessCheckKind::SubjectConformance,
        harness_revision: revision.clone(),
        source_locator: locator,
        source_fingerprint: fingerprint,
        capability_ids: CanonicalSet::new([fixture.definition.capability_id.clone()]),
        execution_target: fixture.target.clone(),
        cli_artifact_digest: cli_digest.clone(),
        verified_artifact_digest: artifact_digest.clone(),
        platform_scope: CanonicalSet::new([fixture.platform.clone()]),
        invocation: command,
        expected_outcome: expected.clone(),
        verification_evidence: None,
        limitations: CanonicalSet::new([NonEmptyText::new(
            "does not exercise initClient or non-linux platforms",
        )
        .unwrap()]),
    };
    let result = HarnessCheckResult {
        check_id: check_id.clone(),
        check_kind: HarnessCheckKind::SubjectConformance,
        harness_revision: revision,
        target: fixture.target.clone(),
        cli_artifact_digest: cli_digest,
        verified_artifact_digest: artifact_digest,
        platform: fixture.platform.clone(),
        outcome: CheckOutcome::Passed,
        assertion: expected.assertion,
        capability_ids: mapping.capability_ids.clone(),
        stdout_digest: Digest::sha256("stdout"),
        stderr_digest: Digest::sha256("stderr"),
    };
    let checks = HarnessCheckInventory {
        checks: BTreeMap::from([(check_id.clone(), source)]),
    };
    let mappings = HarnessMappings {
        checks: BTreeMap::from([(check_id, mapping.clone())]),
    };
    let policy = CommandPolicy {
        programs: BTreeSet::from([ExecutableId::new("dagger").unwrap()]),
        working_directories: BTreeSet::from([RepositoryRelativePath::new("sdk/rust").unwrap()]),
        environment_keys: BTreeSet::from(["DAGGER_LOG_FORMAT".to_owned()]),
    };
    HarnessFixture {
        fixture,
        checks,
        mappings,
        mapping,
        result,
        policy,
    }
}

// Invariant: mappings are an exact source-fingerprint-preserving subject/self partition.
// Feature: rust-sdk-completeness-contract, Property 10: harness inventory partition
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_10_harness_inventory_partition(
        self_check in any::<bool>(),
        mutation in 0_u8..15,
    ) {
        let mut harness = harness_fixture();
        if self_check {
            harness.checks.checks.values_mut().next().unwrap().check_kind =
                HarnessCheckKind::HarnessSelf;
            let mapping = harness.mappings.checks.values_mut().next().unwrap();
            mapping.check_kind = HarnessCheckKind::HarnessSelf;
            mapping.capability_ids = CanonicalSet::default();
            harness.mapping = mapping.clone();
        }
        match mutation {
            1 => harness.mappings.checks.clear(),
            2 => {
                let extra_id = CheckId::new("extra-check").unwrap();
                let mut extra = harness.mapping.clone();
                extra.check_id = extra_id.clone();
                harness.mappings.checks.insert(extra_id, extra);
            }
            3 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .source_fingerprint = Digest::sha256("changed"),
            4 => {
                harness.mappings.checks.values_mut().next().unwrap().check_kind = if self_check {
                    HarnessCheckKind::SubjectConformance
                } else {
                    HarnessCheckKind::HarnessSelf
                };
            }
            5 => {
                harness.mappings.checks.values_mut().next().unwrap().capability_ids =
                    if self_check {
                        CanonicalSet::new([harness.fixture.definition.capability_id.clone()])
                    } else {
                        CanonicalSet::default()
                    };
            }
            6 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .capability_ids = CanonicalSet::new([CapabilityId::new("unknown/capability").unwrap()]),
            7 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .harness_revision = CommitSha::new("d".repeat(40)).unwrap(),
            8 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .execution_target = alternate_target(),
            9 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .platform_scope = CanonicalSet::default(),
            10 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .invocation
                .args
                .reverse(),
            11 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .limitations = CanonicalSet::default(),
            12 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .verification_evidence = Some(EvidenceId::new("unknown/evidence").unwrap()),
            13 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .cli_artifact_digest = Digest::sha256("other-cli"),
            14 => harness
                .mappings
                .checks
                .values_mut()
                .next()
                .unwrap()
                .verified_artifact_digest = Digest::sha256("other-artifact"),
            _ => {}
        }
        let context = HarnessMappingContext {
            harness_revision: &harness.mapping.harness_revision,
            target: &harness.mapping.execution_target,
            cli_artifact_digest: &harness.mapping.cli_artifact_digest,
            verified_artifact_digest: &harness.mapping.verified_artifact_digest,
            command_policy: &harness.policy,
        };

        let result = validate_harness_mappings(
            harness.mappings,
            &harness.checks,
            &harness.fixture.inventory,
            &EvidenceRegistry::default(),
            &context,
        );
        prop_assert_eq!(result.is_ok(), mutation == 0);
    }
}

// Invariant: a result proves only its exact mapped identity, artifact, platform, and assertion.
// Feature: rust-sdk-completeness-contract, Property 11: harness evidence containment
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_11_harness_evidence_containment(mutation in 0_u8..12) {
        let mut harness = harness_fixture();
        match mutation {
            1 => harness.result.check_kind = HarnessCheckKind::HarnessSelf,
            2 => harness.result.harness_revision = CommitSha::new("d".repeat(40)).unwrap(),
            3 => harness.result.target = alternate_target(),
            4 => harness.result.cli_artifact_digest = Digest::sha256("other-cli"),
            5 => harness.result.verified_artifact_digest = Digest::sha256("other-workspace"),
            6 => harness.result.platform.architecture = Architecture::Arm64,
            7 => harness.result.assertion = NonEmptyText::new("another assertion").unwrap(),
            8 => harness.result.capability_ids = CanonicalSet::default(),
            9 => harness.result.outcome = CheckOutcome::Failed,
            10 => {
                harness.mapping.check_kind = HarnessCheckKind::HarnessSelf;
                harness.mapping.capability_ids = CanonicalSet::default();
                harness.result.check_kind = HarnessCheckKind::HarnessSelf;
                harness.result.capability_ids = CanonicalSet::default();
            }
            11 => harness.result.check_id = CheckId::new("another-check").unwrap(),
            _ => {}
        }
        let result = admit_harness_result(&harness.mapping, &harness.result);
        if mutation == 0 {
            prop_assert!(matches!(result, Ok(HarnessAdmission::Passing(_))));
        } else if mutation == 9 {
            let expected_blocker =
                matches!(result, Ok(HarnessAdmission::ExpectedBlocker { .. }));
            prop_assert!(expected_blocker);
        } else {
            prop_assert!(result.is_err());
        }
    }
}

// Invariant: Feature 8 extensions preserve behavior through one public Rust-valid adapter.
// Feature: rust-sdk-completeness-contract, Property 21: portable conformance extensions
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_21_portable_conformance_extensions(
        use_mod_test in any::<bool>(),
        mutation in 0_u8..8,
    ) {
        let fixture = fixture();
        let mut scenario = ConformanceScenario {
            scenario_id: ScenarioId::new("rust-connect").unwrap(),
            source_anchors: CanonicalSet::new([fixture.authority_anchor.clone()]),
            observable_behavior: serde_json::json!({
                "event": "client-connected",
                "result": "query-executed"
            }),
            capability_ids: CanonicalSet::new([fixture.definition.capability_id.clone()]),
            harness_adapter: if use_mod_test {
                HarnessAdapter::ModTest
            } else {
                HarnessAdapter::SdkTarget
            },
            invocation: CommandSpec {
                program: ExecutableId::new("dagger").unwrap(),
                args: vec!["test".to_owned(), "--sdk=rust".to_owned()],
                working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
                environment: BTreeMap::from([("DAGGER_LOG_FORMAT".to_owned(), "plain".to_owned())]),
            },
            expected_outcome: ExpectedOutcome {
                outcome: CheckOutcome::Passed,
                assertion: NonEmptyText::new("client connection remains observable").unwrap(),
            },
        };
        let policy = CommandPolicy {
            programs: BTreeSet::from([ExecutableId::new("dagger").unwrap()]),
            working_directories: BTreeSet::from([
                RepositoryRelativePath::new("sdk/rust").unwrap(),
            ]),
            environment_keys: BTreeSet::from(["DAGGER_LOG_FORMAT".to_owned()]),
        };
        match mutation {
            1 => scenario.capability_ids = CanonicalSet::default(),
            2 => scenario.capability_ids =
                CanonicalSet::new([CapabilityId::new("unknown/capability").unwrap()]),
            3 => scenario.source_anchors = CanonicalSet::default(),
            4 => scenario.observable_behavior = serde_json::json!({"command": "dagger do old"}),
            5 => scenario.invocation.program = ExecutableId::new("go").unwrap(),
            6 => scenario.invocation.args = vec!["do".to_owned()],
            7 => scenario.expected_outcome.outcome = CheckOutcome::Failed,
            _ => {}
        }

        prop_assert_eq!(
            validate_conformance_scenario(&scenario, &fixture.inventory, &policy).is_ok(),
            mutation == 0
        );
    }
}

#[derive(Clone)]
struct FakeExecutor {
    cli_digest: Digest,
    target: TargetDigest,
    artifact_digest: Digest,
    status: Option<i32>,
}

impl HarnessCommandExecutor for FakeExecutor {
    fn cli_artifact_digest(&self) -> Result<Digest, ToolError> {
        Ok(self.cli_digest.clone())
    }

    fn execution_target(&self) -> &TargetDigest {
        &self.target
    }

    fn verified_artifact_digest(&self) -> &Digest {
        &self.artifact_digest
    }

    fn execute(
        &self,
        _command: &CommandSpec,
        _repository_root: &Path,
    ) -> Result<HarnessProcessOutput, ToolError> {
        Ok(HarnessProcessOutput::new(
            self.status,
            b"normalized stdout".to_vec(),
            b"ephemeral stderr".to_vec(),
        ))
    }
}

#[test]
fn per_check_runner_normalizes_expected_subject_failure_without_an_integrity_error() {
    let harness = harness_fixture();
    let executor = FakeExecutor {
        cli_digest: harness.mapping.cli_artifact_digest.clone(),
        target: harness.mapping.execution_target.clone(),
        artifact_digest: harness.mapping.verified_artifact_digest.clone(),
        status: Some(1),
    };

    let context = HarnessRunContext {
        harness_revision: &harness.mapping.harness_revision,
        target: &harness.mapping.execution_target,
        verified_artifact_digest: &harness.mapping.verified_artifact_digest,
        platform: harness.fixture.platform,
        command_policy: &harness.policy,
    };

    let result = run_harness_check(&harness.mapping, &context, &executor, Path::new("."))
        .unwrap()
        .unwrap();

    assert_eq!(result.outcome, CheckOutcome::Failed);
    assert!(matches!(
        admit_harness_result(&harness.mapping, &result),
        Ok(HarnessAdmission::ExpectedBlocker { .. })
    ));
}
