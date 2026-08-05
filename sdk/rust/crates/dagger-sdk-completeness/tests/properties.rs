//! Executable correctness properties for canonical artifacts and immutable target identity.
//!
//! Generators begin with a coherent contract graph, then vary representation order or introduce
//! named, independently selectable drift. This keeps each failure attributable to the invariant
//! under test instead of to unrelated malformed scaffolding.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;

use dagger_sdk_completeness::{
    AuthorityClass, CanonicalSet, CapabilityCandidate, CapabilityDefinition, CapabilityId,
    CapabilityKind, CapabilityOrigin, DiagnosticCode, Digest, DigestDomain, NonEmptyText,
    RepositoryRelativePath, RepositoryRoots, SourceBundle, SourceCoverage, SourceItem,
    SourceItemCoverage, SourceItemDisposition, SourceItemId, SourceItemInventory, SourceItemKind,
    SourceItemState, SourceLoadError, SourceLocator, Stability, build_inventory, canonical_bytes,
    canonical_digest, decode_canonical, load_source_bundles, schema_capability_id,
    semantic_fingerprint, validate_authority_registry, validate_authority_sources,
    validate_source_coverage, validate_target,
};
use proptest::prelude::*;
use proptest::strategy::ValueTree;
use proptest::test_runner::{Config, RngSeed, TestRunner};
use serde::Deserialize;

mod support;

use support::{
    AuthorityCase, AuthorityMutation, DurableModel, PROPTEST_CASES, TargetMutation,
    apply_authority_mutations, apply_target_mutations, authority_case_strategy, authority_id,
    authority_mutation_set_strategy, commit_strategy, dagger_version_strategy, digest_strategy,
    durable_model_strategy, equivalent_contract_cases_strategy, locator_strategy, proptest_config,
    relative_path_strategy, semver_strategy, target_case_strategy, target_mutation_set_strategy,
    text_strategy,
};

#[test]
fn property_configuration_preserves_failures_and_runs_the_required_case_count() {
    let config = proptest_config();

    assert_eq!(config.cases, PROPTEST_CASES);
    assert!(config.failure_persistence.is_some());
}

// Invariant: registry acceptance is exactly total target-bound authority plus contained source.
// Feature: rust-sdk-completeness-contract, Property 3: authority registry totality and containment
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_3_authority_registry_totality_and_containment(
        mut case in authority_case_strategy(),
        mutations in authority_mutation_set_strategy(),
    ) {
        apply_authority_mutations(&mut case, &mutations);
        if mutations.is_empty() {
            for (authority_id, source) in &case.registry.authorities {
                let reversed = SourceBundle::new(
                    case.bundles.bundles()[authority_id]
                        .files()
                        .iter()
                        .rev()
                        .map(|(path, bytes)| (path.clone(), bytes.clone())),
                );
                let recomputed =
                    dagger_sdk_completeness::recompute_source_digest(source, &reversed).unwrap();
                prop_assert_eq!(&recomputed, &source.source_digest);
            }
        }
        let result = case.validate();

        prop_assert_eq!(result.is_ok(), mutations.is_empty());
    }

    #[test]
    fn each_named_authority_mutation_is_rejected_by_its_declared_boundary(
        mut case in authority_case_strategy(),
        mutation in proptest::sample::select(AuthorityMutation::ALL.to_vec()),
    ) {
        apply_authority_mutations(&mut case, &BTreeSet::from([mutation]));
        let diagnostics = case.validate().unwrap_err();
        let actual = diagnostics
            .as_slice()
            .iter()
            .map(|diagnostic| diagnostic.code)
            .collect::<BTreeSet<_>>();

        prop_assert!(actual.contains(&mutation.expected_code()));
    }
}

#[derive(Clone)]
struct InventoryFixture {
    source_items: SourceItemInventory,
    coverage: SourceCoverage,
    candidates: Vec<CapabilityCandidate>,
    capability_id: CapabilityId,
}

fn inventory_fixture(case: &AuthorityCase) -> InventoryFixture {
    let capability_id = CapabilityId::new("behavior/sdk-contract-harness/module-init").unwrap();
    let go_authority = authority_id(AuthorityClass::GoClient);
    let harness_authority = authority_id(AuthorityClass::SdkContractHarness);
    let go_source_id = SourceItemId::new("source/go-client/module-init").unwrap();
    let harness_source_id = SourceItemId::new("source/sdk-contract-harness/module-init").unwrap();
    let excluded_source_id = SourceItemId::new("source/go-client/generated-binding").unwrap();
    let schema_source_id =
        SourceItemId::new("source/engine-schema/schema-type/object/%47enerated").unwrap();
    let schema_capability_id = schema_capability_id(
        &authority_id(AuthorityClass::EngineSchema),
        "schema-type",
        &["object", "Generated"],
    )
    .unwrap();
    let go_signature = serde_json::json!({"contract": "module-init", "shape": "go"});
    let harness_signature =
        serde_json::json!({"contract": "module-init", "shape": "language-neutral"});

    let go_item = fixture_source_item(
        go_source_id.clone(),
        go_authority.clone(),
        SourceItemState::Active,
        go_signature.clone(),
    );
    let harness_item = fixture_source_item(
        harness_source_id.clone(),
        harness_authority.clone(),
        SourceItemState::Active,
        harness_signature.clone(),
    );
    let excluded_item = fixture_source_item(
        excluded_source_id.clone(),
        go_authority.clone(),
        SourceItemState::Active,
        serde_json::json!({"generated": true}),
    );
    let mut schema_item = fixture_source_item(
        schema_source_id.clone(),
        authority_id(AuthorityClass::EngineSchema),
        SourceItemState::Active,
        serde_json::json!({"kind": "OBJECT", "name": "Generated"}),
    );
    schema_item.item_kind = SourceItemKind::new("schema-type").unwrap();
    let go_source = &case.registry.authorities[&go_authority];
    let harness_source = &case.registry.authorities[&harness_authority];
    let exclusion = go_source.exclude.as_slice()[0].clone();

    InventoryFixture {
        source_items: SourceItemInventory {
            items: BTreeMap::from([
                (go_source_id.clone(), go_item),
                (harness_source_id.clone(), harness_item),
                (excluded_source_id.clone(), excluded_item),
                (schema_source_id.clone(), schema_item),
            ]),
        },
        coverage: SourceCoverage {
            items: BTreeMap::from([
                (
                    go_source_id.clone(),
                    SourceItemCoverage {
                        source_item_id: go_source_id.clone(),
                        selected_by: go_source.include.as_slice()[0].clone(),
                        disposition: SourceItemDisposition::Reference(CanonicalSet::new([
                            capability_id.clone(),
                        ])),
                    },
                ),
                (
                    harness_source_id.clone(),
                    SourceItemCoverage {
                        source_item_id: harness_source_id.clone(),
                        selected_by: harness_source.include.as_slice()[0].clone(),
                        disposition: SourceItemDisposition::Primary(CanonicalSet::new([
                            capability_id.clone(),
                        ])),
                    },
                ),
                (
                    excluded_source_id.clone(),
                    SourceItemCoverage {
                        source_item_id: excluded_source_id,
                        selected_by: exclusion.selector.clone(),
                        disposition: SourceItemDisposition::Excluded(exclusion),
                    },
                ),
                (
                    schema_source_id.clone(),
                    SourceItemCoverage {
                        source_item_id: schema_source_id,
                        selected_by: case.registry.authorities
                            [&authority_id(AuthorityClass::EngineSchema)]
                            .include
                            .as_slice()[0]
                            .clone(),
                        disposition: SourceItemDisposition::Primary(CanonicalSet::new([
                            schema_capability_id,
                        ])),
                    },
                ),
            ]),
        },
        candidates: vec![
            CapabilityCandidate {
                definition: fixture_definition(
                    capability_id.clone(),
                    go_authority,
                    go_source_id,
                    go_signature,
                ),
                origin: CapabilityOrigin::Go,
                common_contract: true,
                target_compatible: true,
            },
            CapabilityCandidate {
                definition: fixture_definition(
                    capability_id.clone(),
                    harness_authority,
                    harness_source_id,
                    harness_signature,
                ),
                origin: CapabilityOrigin::Harness,
                common_contract: true,
                target_compatible: true,
            },
        ],
        capability_id,
    }
}

fn fixture_source_item(
    source_item_id: SourceItemId,
    authority_id: dagger_sdk_completeness::AuthorityId,
    state: SourceItemState,
    signature: serde_json::Value,
) -> SourceItem {
    SourceItem {
        locator: SourceLocator::new(format!("fixture#{}", source_item_id.as_str())).unwrap(),
        fingerprint: semantic_fingerprint(&signature).unwrap(),
        source_item_id,
        authority_id,
        item_kind: SourceItemKind::new("behavior/fixture").unwrap(),
        semantic_signature: signature,
        state,
    }
}

fn fixture_definition(
    capability_id: CapabilityId,
    authority_id: dagger_sdk_completeness::AuthorityId,
    source_item_id: SourceItemId,
    signature: serde_json::Value,
) -> CapabilityDefinition {
    CapabilityDefinition {
        capability_id,
        authority_id,
        capability_kind: CapabilityKind::new("behavior/lifecycle").unwrap(),
        source_item_ids: CanonicalSet::new([source_item_id]),
        source_anchors: CanonicalSet::default(),
        summary: NonEmptyText::new("Module initialization contract").unwrap(),
        capability_fingerprint: semantic_fingerprint(&signature).unwrap(),
        semantic_signature: signature,
        stability: Stability::Stable,
    }
}

#[test]
fn fixture_inventory_has_zero_uncovered_source_items() {
    let case = one_authority_case();
    let sources = case.clone().validate().unwrap();
    let fixture = inventory_fixture(&case);
    let coverage =
        validate_source_coverage(&sources, &fixture.source_items, fixture.coverage).unwrap();
    let inventory = build_inventory(&fixture.source_items, &coverage, fixture.candidates).unwrap();

    assert_eq!(inventory.capabilities.len(), 2);
    assert_eq!(
        coverage.as_inner().items.len(),
        fixture.source_items.items.len()
    );
}

// Invariant: every selected item is primary, reference, or an exact reviewed exclusion.
// Feature: rust-sdk-completeness-contract, Property 5: exhaustive source-item coverage
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_5_exhaustive_source_item_coverage(
        case in authority_case_strategy(),
        mutation in 0_u8..5,
    ) {
        let sources = case.clone().validate().unwrap();
        let mut fixture = inventory_fixture(&case);
        match mutation {
            1 => {
                let source_id = fixture.coverage.items.keys().next().unwrap().clone();
                fixture.coverage.items.remove(&source_id);
            }
            2 => {
                let source_id = SourceItemId::new("source/uncovered/extra").unwrap();
                fixture.source_items.items.insert(
                    source_id.clone(),
                    fixture_source_item(
                        source_id,
                        authority_id(AuthorityClass::EngineSchema),
                        SourceItemState::Active,
                        serde_json::json!({"extra": true}),
                    ),
                );
            }
            3 => fixture.candidates.clear(),
            4 => {
                let source_id =
                    SourceItemId::new("source/sdk-contract-harness/module-init").unwrap();
                fixture.source_items.items.get_mut(&source_id).unwrap().state =
                    SourceItemState::Removed;
            }
            _ => {}
        }
        let coverage =
            validate_source_coverage(&sources, &fixture.source_items, fixture.coverage);
        let result = coverage.and_then(|coverage| {
            build_inventory(&fixture.source_items, &coverage, fixture.candidates)
        });

        prop_assert_eq!(result.is_ok(), mutation == 0);
    }
}

// Invariant: compatible harness semantics win visibly; conflicts and target drift never do.
// Feature: rust-sdk-completeness-contract, Property 12: authority precedence without silent conflict
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_12_authority_precedence_without_silent_conflict(
        case in authority_case_strategy(),
        scenario in 0_u8..4,
        competing_value in any::<u64>(),
    ) {
        let sources = case.clone().validate().unwrap();
        let mut fixture = inventory_fixture(&case);
        match scenario {
            1 => fixture.candidates[1].target_compatible = false,
            2 => {
                fixture.candidates.remove(1);
            }
            3 => {
                let source_item_id =
                    SourceItemId::new("source/sdk-contract-harness/module-init-competing").unwrap();
                let signature =
                    serde_json::json!({"contract": "module-init", "competing": competing_value});
                fixture.source_items.items.insert(
                    source_item_id.clone(),
                    fixture_source_item(
                        source_item_id.clone(),
                        authority_id(AuthorityClass::SdkContractHarness),
                        SourceItemState::Active,
                        signature.clone(),
                    ),
                );
                let harness = &case.registry.authorities
                    [&authority_id(AuthorityClass::SdkContractHarness)];
                fixture.coverage.items.insert(
                    source_item_id.clone(),
                    SourceItemCoverage {
                        source_item_id: source_item_id.clone(),
                        selected_by: harness.include.as_slice()[0].clone(),
                        disposition: SourceItemDisposition::Primary(CanonicalSet::new([
                            fixture.capability_id.clone(),
                        ])),
                    },
                );
                fixture.candidates.push(CapabilityCandidate {
                    definition: fixture_definition(
                        fixture.capability_id.clone(),
                        authority_id(AuthorityClass::SdkContractHarness),
                        source_item_id,
                        signature,
                    ),
                    origin: CapabilityOrigin::Harness,
                    common_contract: true,
                    target_compatible: true,
                });
            }
            _ => {}
        }
        let coverage =
            validate_source_coverage(&sources, &fixture.source_items, fixture.coverage).unwrap();
        let result = build_inventory(&fixture.source_items, &coverage, fixture.candidates);

        if scenario == 0 {
            let inventory = result.unwrap();
            let definition = &inventory.capabilities[&fixture.capability_id];
            prop_assert_eq!(
                &definition.authority_id,
                &authority_id(AuthorityClass::SdkContractHarness)
            );
            prop_assert_eq!(definition.source_item_ids.as_slice().len(), 2);
        } else {
            let diagnostics = result.unwrap_err();
            let expected = match scenario {
                1 => DiagnosticCode::SdkContractTargetMismatch,
                _ => DiagnosticCode::CapabilityDuplicate,
            };
            let has_expected = diagnostics
                .as_slice()
                .iter()
                .any(|diagnostic| diagnostic.code == expected)
                || diagnostics
                    .as_slice()
                    .iter()
                    .any(|diagnostic| diagnostic.code == DiagnosticCode::CapabilitySourceMissing);
            prop_assert!(has_expected);
        }
    }
}

// Invariant: equal colliding definitions merge; unequal definitions fail under the same ID.
// Feature: rust-sdk-completeness-contract, Property 6: stable capability identity and semantic fingerprinting
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_6_colliding_capability_signatures(
        case in authority_case_strategy(),
        equal_signature in any::<bool>(),
        changed_value in any::<u64>(),
    ) {
        let sources = case.clone().validate().unwrap();
        let mut fixture = inventory_fixture(&case);
        let original_harness = fixture.candidates[1].clone();
        let source_item_id =
            SourceItemId::new("source/sdk-contract-harness/module-init-collision").unwrap();
        let signature = if equal_signature {
            original_harness.definition.semantic_signature.clone()
        } else {
            serde_json::json!({"contract": "module-init", "changed": changed_value})
        };
        fixture.source_items.items.insert(
            source_item_id.clone(),
            fixture_source_item(
                source_item_id.clone(),
                authority_id(AuthorityClass::SdkContractHarness),
                SourceItemState::Active,
                signature.clone(),
            ),
        );
        let harness =
            &case.registry.authorities[&authority_id(AuthorityClass::SdkContractHarness)];
        fixture.coverage.items.insert(
            source_item_id.clone(),
            SourceItemCoverage {
                source_item_id: source_item_id.clone(),
                selected_by: harness.include.as_slice()[0].clone(),
                disposition: SourceItemDisposition::Primary(CanonicalSet::new([
                    fixture.capability_id.clone(),
                ])),
            },
        );
        fixture.candidates.push(CapabilityCandidate {
            definition: fixture_definition(
                fixture.capability_id,
                authority_id(AuthorityClass::SdkContractHarness),
                source_item_id,
                signature,
            ),
            origin: CapabilityOrigin::Harness,
            common_contract: true,
            target_compatible: true,
        });
        let coverage =
            validate_source_coverage(&sources, &fixture.source_items, fixture.coverage).unwrap();
        let result = build_inventory(&fixture.source_items, &coverage, fixture.candidates);

        prop_assert_eq!(result.is_ok(), equal_signature);
    }
}

// Invariant: canonical path checks reject symlink escape before reading selected bytes.
// Feature: rust-sdk-completeness-contract, Property 3: authority registry totality and containment
#[cfg(unix)]
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_3_local_loader_rejects_symlink_escape(
        case in authority_case_strategy(),
        escape in any::<bool>(),
    ) {
        use std::os::unix::fs::symlink;

        let temp = tempfile::tempdir().unwrap();
        let roots = materialize_authority_case(&case, temp.path());
        if escape {
            let engine_id = authority_id(AuthorityClass::EngineSchema);
            let source_path = case.bundles.bundles()[&engine_id]
                .files()
                .keys()
                .next()
                .unwrap();
            let repository_root = roots
                .get(&case.registry.authorities[&engine_id].repository)
                .unwrap();
            let selected_path = repository_root.join(source_path.as_str());
            let selected_bytes = fs::read(&selected_path).unwrap();
            let outside_path = temp.path().join("outside-authority.rs");
            fs::write(&outside_path, selected_bytes).unwrap();
            fs::remove_file(&selected_path).unwrap();
            symlink(&outside_path, &selected_path).unwrap();
        }

        let target = case.validated_target();
        let registry =
            validate_authority_registry(&target, case.registry.clone()).unwrap();
        let loaded = load_source_bundles(&registry, &roots);
        if escape {
            let SourceLoadError::Contract(diagnostics) = loaded.unwrap_err() else {
                prop_assert!(false, "symlink escape must be a contract diagnostic");
                return Ok(());
            };
            let has_containment_diagnostic = diagnostics.as_slice().iter().any(|diagnostic| {
                diagnostic.code == DiagnosticCode::AuthorityRepositoryInvalid
            });
            prop_assert!(has_containment_diagnostic);
        } else {
            let bundles = loaded.unwrap();
            prop_assert!(validate_authority_sources(registry, bundles).is_ok());
        }
    }
}

#[test]
fn repository_relative_paths_reject_parent_traversal_before_loading() {
    assert!(serde_json::from_str::<RepositoryRelativePath>("\"../outside.rs\"").is_err());
}

#[test]
fn source_coverage_is_uniform_across_every_lifecycle_state() {
    let case = one_authority_case();
    let sources = case.clone().validate().unwrap();
    let engine_id = authority_id(AuthorityClass::EngineSchema);
    let engine_source = &case.registry.authorities[&engine_id];
    let selected_by = engine_source.include.as_slice()[0].clone();
    let mut items = BTreeMap::new();
    let mut coverage = BTreeMap::new();

    for (index, state) in [
        SourceItemState::Active,
        SourceItemState::Deprecated,
        SourceItemState::Skipped,
        SourceItemState::Removed,
        SourceItemState::HarnessSelf,
    ]
    .into_iter()
    .enumerate()
    {
        let source_item_id = SourceItemId::new(format!("source/state-{index}")).unwrap();
        let capability_id = CapabilityId::new(format!("capability/state-{index}")).unwrap();
        let locator = SourceLocator::new(format!("source.rs#State{index}")).unwrap();
        items.insert(
            source_item_id.clone(),
            SourceItem {
                source_item_id: source_item_id.clone(),
                authority_id: engine_id.clone(),
                item_kind: SourceItemKind::new("fixture/item").unwrap(),
                locator,
                semantic_signature: serde_json::json!({"state": index}),
                fingerprint: Digest::sha256(format!("state-{index}")),
                state,
            },
        );
        coverage.insert(
            source_item_id.clone(),
            SourceItemCoverage {
                source_item_id,
                selected_by: selected_by.clone(),
                disposition: SourceItemDisposition::Primary(CanonicalSet::new([capability_id])),
            },
        );
    }

    let go_id = authority_id(AuthorityClass::GoClient);
    let go_source = &case.registry.authorities[&go_id];
    let exclusion = go_source.exclude.as_slice()[0].clone();
    let excluded_id = SourceItemId::new("source/reviewed-exclusion").unwrap();
    items.insert(
        excluded_id.clone(),
        SourceItem {
            source_item_id: excluded_id.clone(),
            authority_id: go_id,
            item_kind: SourceItemKind::new("fixture/excluded").unwrap(),
            locator: SourceLocator::new("source.rs#GeneratedBinding").unwrap(),
            semantic_signature: serde_json::json!({"excluded": true}),
            fingerprint: Digest::sha256("reviewed-exclusion"),
            state: SourceItemState::Active,
        },
    );
    coverage.insert(
        excluded_id.clone(),
        SourceItemCoverage {
            source_item_id: excluded_id,
            selected_by: exclusion.selector.clone(),
            disposition: SourceItemDisposition::Excluded(exclusion),
        },
    );

    assert!(
        validate_source_coverage(
            &sources,
            &SourceItemInventory { items },
            SourceCoverage { items: coverage },
        )
        .is_ok()
    );
}

#[test]
fn source_coverage_rejects_uncovered_items_and_stale_exclusions() {
    let case = one_authority_case();
    let sources = case.clone().validate().unwrap();
    let source_item_id = SourceItemId::new("source/uncovered").unwrap();
    let inventory = SourceItemInventory {
        items: BTreeMap::from([(
            source_item_id.clone(),
            SourceItem {
                source_item_id,
                authority_id: authority_id(AuthorityClass::EngineSchema),
                item_kind: SourceItemKind::new("fixture/item").unwrap(),
                locator: SourceLocator::new("source.rs#Uncovered").unwrap(),
                semantic_signature: serde_json::json!({"covered": false}),
                fingerprint: Digest::sha256("uncovered"),
                state: SourceItemState::Active,
            },
        )]),
    };
    let diagnostics =
        validate_source_coverage(&sources, &inventory, SourceCoverage::default()).unwrap_err();
    let codes = diagnostics
        .as_slice()
        .iter()
        .map(|diagnostic| diagnostic.code)
        .collect::<BTreeSet<_>>();

    assert!(codes.contains(&DiagnosticCode::CapabilitySourceMissing));
    assert!(codes.contains(&DiagnosticCode::AuthorityExclusionInvalid));
}

fn one_authority_case() -> AuthorityCase {
    let mut runner = TestRunner::new(Config {
        cases: 1,
        failure_persistence: None,
        rng_seed: RngSeed::Fixed(0xa11_7a0),
        ..Config::default()
    });
    authority_case_strategy()
        .new_tree(&mut runner)
        .unwrap()
        .current()
}

fn materialize_authority_case(case: &AuthorityCase, parent: &std::path::Path) -> RepositoryRoots {
    let repositories = [
        case.target.target.dagger_repository.clone(),
        case.target.target.go_sdk_repository.clone(),
        case.target.target.sdk_contract_repository.clone(),
    ];
    let roots = repositories
        .into_iter()
        .enumerate()
        .map(|(index, repository)| {
            let root = parent.join(format!("repository-{index}"));
            fs::create_dir_all(&root).unwrap();
            (repository, root)
        })
        .collect::<BTreeMap<_, _>>();

    for (authority_id, bundle) in case.bundles.bundles() {
        let repository = &case.registry.authorities[authority_id].repository;
        let root = &roots[repository];
        for (path, bytes) in bundle.files() {
            let destination = root.join(path.as_str());
            fs::create_dir_all(destination.parent().unwrap()).unwrap();
            fs::write(destination, bytes).unwrap();
        }
    }

    RepositoryRoots::new(roots)
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct TargetRegressionCase {
    name: String,
    mutations: BTreeSet<TargetMutation>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct AuthorityRegressionCase {
    name: String,
    mutations: BTreeSet<AuthorityMutation>,
}

#[test]
fn deterministic_authority_regression_corpus_replays() {
    let corpus = serde_json::from_str::<Vec<AuthorityRegressionCase>>(include_str!(
        "fixtures/authority-regressions.json"
    ))
    .unwrap();
    let mut runner = TestRunner::new(Config {
        cases: 1,
        failure_persistence: None,
        rng_seed: RngSeed::Fixed(0xa11_7a03),
        ..Config::default()
    });
    let reference = authority_case_strategy()
        .new_tree(&mut runner)
        .unwrap()
        .current();

    for regression in corpus {
        let mut case = reference.clone();
        apply_authority_mutations(&mut case, &regression.mutations);
        let diagnostics = case.validate().unwrap_err();
        for mutation in regression.mutations {
            assert!(
                diagnostics
                    .as_slice()
                    .iter()
                    .any(|diagnostic| diagnostic.code == mutation.expected_code()),
                "regression case {} did not produce {}",
                regression.name,
                mutation.expected_code()
            );
        }
    }
}

#[test]
fn deterministic_target_regression_corpus_replays() {
    let corpus = serde_json::from_str::<Vec<TargetRegressionCase>>(include_str!(
        "fixtures/target-regressions.json"
    ))
    .unwrap();
    let mut runner = TestRunner::new(Config {
        cases: 1,
        failure_persistence: None,
        rng_seed: RngSeed::Fixed(0xd_a66e_1f15),
        ..Config::default()
    });
    let reference = target_case_strategy()
        .new_tree(&mut runner)
        .unwrap()
        .current();

    for regression in corpus {
        let mut case = reference.clone();
        let expected = apply_target_mutations(&mut case, &regression.mutations);
        let actual = match validate_target(case.target, &case.observation) {
            Ok(_) => Vec::new(),
            Err(diagnostics) => diagnostics
                .as_slice()
                .iter()
                .map(|diagnostic| diagnostic.code)
                .collect(),
        };

        assert_eq!(actual, expected, "regression case {}", regression.name);
    }
}

proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn generated_scalars_use_production_canonical_forms(
        commit in commit_strategy(),
        semver in semver_strategy(),
        dagger_version in dagger_version_strategy(),
        digest in digest_strategy(),
        text in text_strategy(),
        path in relative_path_strategy(),
        locator in locator_strategy(),
    ) {
        prop_assert_eq!(commit.as_str().len(), 40);
        prop_assert!(!semver.to_string().starts_with('v'));
        prop_assert!(dagger_version.to_string().starts_with('v'));
        prop_assert_eq!(digest.as_str().len(), "sha256:".len() + 64);
        prop_assert!(!text.as_str().is_empty());
        prop_assert!(!path.as_str().starts_with('/'));
        prop_assert!(!locator.as_str().starts_with('/'));
    }

    #[test]
    fn every_generated_durable_model_has_a_typed_canonical_round_trip(
        model in durable_model_strategy(),
    ) {
        let bytes = canonical_bytes(&model).unwrap();
        let decoded = decode_canonical::<DurableModel>(&bytes).unwrap();

        prop_assert_eq!(decoded, model);
    }
}

// Invariant: enumeration order cannot change canonical bytes, digests, or decoded meaning.
// Feature: rust-sdk-completeness-contract, Property 1: canonical artifact determinism
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_1_canonical_artifact_determinism(
        cases in equivalent_contract_cases_strategy(),
    ) {
        prop_assert_eq!(cases.forward.reference_errors(), Vec::<&str>::new());
        prop_assert_eq!(cases.reverse.reference_errors(), Vec::<&str>::new());
        prop_assert_eq!(&cases.forward, &cases.reverse);

        let forward_models = cases.forward.durable_models();
        let reverse_models = cases.reverse.durable_models();
        prop_assert_eq!(forward_models.len(), DurableModel::VARIANT_COUNT);
        prop_assert_eq!(reverse_models.len(), DurableModel::VARIANT_COUNT);

        let kinds = forward_models
            .iter()
            .map(DurableModel::kind)
            .collect::<BTreeSet<_>>();
        prop_assert_eq!(kinds.len(), DurableModel::VARIANT_COUNT);

        for (forward, reverse) in forward_models.into_iter().zip(reverse_models) {
            prop_assert_eq!(forward.kind(), reverse.kind());
            let forward_bytes = canonical_bytes(&forward).unwrap();
            let reverse_bytes = canonical_bytes(&reverse).unwrap();
            prop_assert_eq!(&forward_bytes, &reverse_bytes);

            let round_trip = decode_canonical::<DurableModel>(&forward_bytes).unwrap();
            prop_assert_eq!(round_trip, forward.clone());

            for domain in [
                DigestDomain::Target,
                DigestDomain::Source,
                DigestDomain::Capability,
                DigestDomain::Artifact,
                DigestDomain::RuleExpansion,
                DigestDomain::Compatibility,
            ] {
                prop_assert_eq!(
                    canonical_digest(domain, &forward).unwrap(),
                    canonical_digest(domain, &reverse).unwrap()
                );
            }

            prop_assert_ne!(
                canonical_digest(DigestDomain::Target, &forward).unwrap(),
                canonical_digest(DigestDomain::Source, &forward).unwrap()
            );
        }
    }
}

// Invariant: validation succeeds exactly when every observed immutable target fact agrees.
// Feature: rust-sdk-completeness-contract, Property 2: immutable target identity
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_2_immutable_target_identity(
        mut case in target_case_strategy(),
        mutations in target_mutation_set_strategy(),
    ) {
        let expected = apply_target_mutations(&mut case, &mutations);
        let result = validate_target(case.target, &case.observation);

        if expected.is_empty() {
            prop_assert!(result.is_ok());
        } else {
            let actual = result
                .unwrap_err()
                .as_slice()
                .iter()
                .map(|diagnostic| diagnostic.code)
                .collect::<Vec<DiagnosticCode>>();
            prop_assert_eq!(actual, expected);
        }
    }

    #[test]
    fn each_named_target_mutation_has_one_declared_effect(
        mut case in target_case_strategy(),
        mutation in proptest::sample::select(TargetMutation::ALL.to_vec()),
    ) {
        let mutations = BTreeSet::from([mutation]);
        let expected = apply_target_mutations(&mut case, &mutations);
        let actual = validate_target(case.target, &case.observation)
            .unwrap_err()
            .as_slice()
            .iter()
            .map(|diagnostic| diagnostic.code)
            .collect::<Vec<_>>();

        prop_assert_eq!(&expected, &vec![mutation.expected_code()]);
        prop_assert_eq!(actual, expected);
    }
}
