//! Observable-authority translation and fixed-family boundary properties.

use dagger_sdk_completeness::extract::go::{GoHelperOutput, go_scenario_context_index};
use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config as ProptestConfig, TestRunner};

const COMPLETENESS: &str = "../../completeness";

fn artifact(path: &str) -> Vec<u8> {
    std::fs::read(format!(
        "{}/{COMPLETENESS}/{path}",
        env!("CARGO_MANIFEST_DIR")
    ))
    .expect("checked conformance artifact is readable")
}

fn checked_plan() -> ReviewedCatalogPlan {
    let ledger: ResolvedLedger = decode_canonical(&artifact("artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&artifact("conformance-scope.json")).unwrap();
    let applicability: ConformanceScopeInput =
        decode_canonical(&artifact("conformance-applicability.json")).unwrap();
    let scope = derive_conformance_scope(&ledger, &reviewed, applicability).unwrap();
    let harness: HarnessMappings = decode_canonical(&artifact("harness-mappings.json")).unwrap();
    let engine: EngineIntegrationMappings =
        decode_canonical(&artifact("engine-integration-mappings.json")).unwrap();
    build_reviewed_catalog_plan(
        &std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../.."),
        &ledger,
        &scope,
        &harness,
        &engine,
        SubjectIdentity::SourceDigest(Digest::sha256("checked Rust source fixture")),
    )
    .unwrap()
}

fn checked_scenario_contexts() -> std::collections::BTreeMap<(SourceLocator, SourceItemKind), Digest>
{
    let output: GoHelperOutput =
        serde_json::from_slice(&artifact("sources/go/go-integration-tests.json")).unwrap();
    go_scenario_context_index(&output).unwrap()
}

#[test]
fn checked_integration_fixtures_have_one_rust_owned_program_each() {
    let plan = checked_plan();
    let registry = compile_observable_fixture_program_registry(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
    )
    .unwrap();
    let artifact = build_observable_fixture_program_artifact(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
    )
    .unwrap();
    assert_eq!(registry.programs().len(), 612);
    assert_eq!(artifact.programs.len(), 612);
    assert_eq!(artifact.program_registry_digest, *registry.digest());

    let integration_cases = plan
        .case_catalog
        .cases()
        .values()
        .filter(|case| case.family == CaseFamily::IntegrationAssertion)
        .collect::<Vec<_>>();
    assert_eq!(integration_cases.len(), 612);
    for case in integration_cases {
        let program = registry.programs().get(&case.fixture_id).unwrap();
        assert_eq!(program.case_id, case.id);
        assert_eq!(program.assertion_ids, case.assertion_ids);
        assert_eq!(program.capability_ids, case.capability_ids);
        assert_eq!(program.fixture_digest, case.fixture_digest);
    }
}

#[test]
fn rust_first_manifest_requires_one_registered_realization_per_scenario() {
    let plan = checked_plan();
    let candidates = scaffold_rust_first_conformance_manifest(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
        &checked_scenario_contexts(),
    )
    .unwrap();
    let runner_source_digest = Digest::sha256("checked scenario runner source");
    let empty_registry = compile_rust_scenario_registry(
        scaffold_rust_scenario_registry(&candidates, runner_source_digest.clone()).unwrap(),
        &candidates,
        &plan.case_catalog,
        &runner_source_digest,
    )
    .unwrap();
    assert_eq!(candidates.scenarios.len(), 612);
    assert!(
        compile_rust_first_conformance_manifest(
            candidates.clone(),
            &plan.assertion_catalog,
            &plan.fixture_registry,
            &plan.case_catalog,
            &empty_registry,
        )
        .is_err(),
        "realization-required candidates must never become executable evidence"
    );

    let mut registrations = Vec::with_capacity(candidates.scenarios.len());
    for (index, scenario) in candidates.scenarios.iter().enumerate() {
        let case = plan.case_catalog.cases().get(&scenario.spine.id).unwrap();
        let CaseProgram::IntegrationAssertion { fixture } = &case.program else {
            panic!("scaffold selected a non-integration case");
        };
        let realization_id =
            ScenarioRealizationId::new(format!("realization/integration/{index:04}")).unwrap();
        registrations.push(RustScenarioRegistration {
            scenario_id: scenario.spine.id.clone(),
            contract_digest: rust_scenario_contract_digest(&scenario.spine).unwrap(),
            proof_id: reviewed_scenario_proof_id(&scenario.spine.expected).unwrap(),
            realization: RustScenarioRealization::ReviewedRustFixture {
                realization_id,
                fixture_id: fixture.clone(),
            },
        });
    }
    let mut registry_input =
        scaffold_rust_scenario_registry(&candidates, runner_source_digest.clone()).unwrap();
    registry_input.registrations = registrations;
    let registry = compile_rust_scenario_registry(
        registry_input,
        &candidates,
        &plan.case_catalog,
        &runner_source_digest,
    )
    .unwrap();
    let realized = apply_rust_scenario_registry(candidates, &registry);
    let manifest = compile_rust_first_conformance_manifest(
        realized,
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
        &registry,
    )
    .unwrap();
    assert_eq!(manifest.scenarios().len(), 612);
}

#[test]
fn rust_scenario_registry_shares_reviewed_executors_but_rejects_stale_and_unselected_bindings() {
    let plan = checked_plan();
    let candidates = scaffold_rust_first_conformance_manifest(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
        &checked_scenario_contexts(),
    )
    .unwrap();
    let runner_source_digest = Digest::sha256("checked scenario runner source");
    let registrations = candidates
        .scenarios
        .iter()
        .take(2)
        .enumerate()
        .map(|(index, scenario)| {
            let case = plan.case_catalog.cases().get(&scenario.spine.id).unwrap();
            let CaseProgram::IntegrationAssertion { fixture } = &case.program else {
                panic!("scaffold selected a non-integration case");
            };
            RustScenarioRegistration {
                scenario_id: scenario.spine.id.clone(),
                contract_digest: rust_scenario_contract_digest(&scenario.spine).unwrap(),
                proof_id: reviewed_scenario_proof_id(&scenario.spine.expected).unwrap(),
                realization: RustScenarioRealization::ReviewedRustFixture {
                    realization_id: ScenarioRealizationId::new(format!(
                        "realization/reviewed/{index:04}"
                    ))
                    .unwrap(),
                    fixture_id: fixture.clone(),
                },
            }
        })
        .collect::<Vec<_>>();
    let mut input =
        scaffold_rust_scenario_registry(&candidates, runner_source_digest.clone()).unwrap();
    input.registrations = registrations;
    compile_rust_scenario_registry(
        input.clone(),
        &candidates,
        &plan.case_catalog,
        &runner_source_digest,
    )
    .unwrap();

    let mut stale = input.clone();
    stale.runner_source_digest = Digest::sha256("different runner source");
    assert!(
        compile_rust_scenario_registry(
            stale,
            &candidates,
            &plan.case_catalog,
            &runner_source_digest,
        )
        .is_err()
    );

    let mut wrong_contract = input.clone();
    wrong_contract.registrations[0].contract_digest = Digest::sha256("different scenario spine");
    assert!(
        compile_rust_scenario_registry(
            wrong_contract,
            &candidates,
            &plan.case_catalog,
            &runner_source_digest,
        )
        .is_err()
    );

    let mut wrong_proof = input.clone();
    wrong_proof.registrations[0].proof_id =
        ScenarioProofId::new("probe/typed-error/category").unwrap();
    if wrong_proof.registrations[0].proof_id
        == reviewed_scenario_proof_id(&candidates.scenarios[0].spine.expected).unwrap()
    {
        wrong_proof.registrations[0].proof_id =
            ScenarioProofId::new("probe/result/exact-value").unwrap();
    }
    assert!(
        compile_rust_scenario_registry(
            wrong_proof,
            &candidates,
            &plan.case_catalog,
            &runner_source_digest,
        )
        .is_err()
    );

    let mut shared = input.clone();
    let shared_id = match &shared.registrations[0].realization {
        RustScenarioRealization::ReviewedRustFixture { realization_id, .. } => {
            realization_id.clone()
        }
        _ => unreachable!("test registrations are reviewed fixtures"),
    };
    let RustScenarioRealization::ReviewedRustFixture { realization_id, .. } =
        &mut shared.registrations[1].realization
    else {
        unreachable!("test registrations are reviewed fixtures")
    };
    *realization_id = shared_id;
    let shared = compile_rust_scenario_registry(
        shared,
        &candidates,
        &plan.case_catalog,
        &runner_source_digest,
    )
    .unwrap();
    assert_eq!(shared.realization_ids().len(), 1);

    let mut unselected = input;
    unselected.registrations[0].scenario_id =
        SignoffCaseId::new("case/integration/unselected").unwrap();
    assert!(
        compile_rust_scenario_registry(
            unselected,
            &candidates,
            &plan.case_catalog,
            &runner_source_digest,
        )
        .is_err()
    );
}

#[test]
fn property_12_authority_translates_to_observable_rust() {
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 128,
        ..ProptestConfig::default()
    });
    runner
        .run(&(0_u8..3, 0_u8..8), |(mechanism, mutation)| {
            let authority_mechanism = match mechanism {
                0 => AuthorityMechanism::SharedSdkContract,
                1 => AuthorityMechanism::NonIdiomaticForRust,
                _ => AuthorityMechanism::ForeignSdkOnly,
            };
            let foreign = authority_mechanism == AuthorityMechanism::ForeignSdkOnly;
            let idiomatic = authority_mechanism == AuthorityMechanism::NonIdiomaticForRust;
            let mut observation = AuthorityTranslationObservation {
                authority_mechanism,
                rust_boundary: (!foreign)
                    .then_some(RustObservableBoundary::ProductionModuleDispatcher),
                predicate: ObservablePredicate::Result(ResultObservation::ExactValue),
                equivalence_decision: idiomatic
                    .then(|| SourceLocator::new("authority/observable#idiomatic").unwrap()),
                shared_invariant_routed: true,
                outcome: if foreign {
                    ObservableEvidenceOutcome::JustifiedInapplicable
                } else {
                    ObservableEvidenceOutcome::Passed
                },
                capability_evidence_complete: true,
                drift: ObservableAuthorityDrift::None,
            };
            match mutation {
                0 => {}
                1 => observation.rust_boundary = None,
                2 => observation.rust_boundary = Some(RustObservableBoundary::SharedBaselineCli),
                3 => observation.equivalence_decision = None,
                4 => observation.outcome = ObservableEvidenceOutcome::Failed,
                5 => observation.shared_invariant_routed = false,
                6 => observation.capability_evidence_complete = false,
                _ => observation.drift = ObservableAuthorityDrift::Reclassified,
            }
            let model_accepts = match authority_mechanism {
                AuthorityMechanism::SharedSdkContract => {
                    observation.rust_boundary.is_some()
                        && observation.equivalence_decision.is_none()
                        && observation.outcome == ObservableEvidenceOutcome::Passed
                }
                AuthorityMechanism::NonIdiomaticForRust => {
                    observation.rust_boundary.is_some()
                        && observation.equivalence_decision.is_some()
                        && observation.outcome == ObservableEvidenceOutcome::Passed
                }
                AuthorityMechanism::ForeignSdkOnly => {
                    observation.rust_boundary.is_none()
                        && observation.equivalence_decision.is_none()
                        && observation.outcome == ObservableEvidenceOutcome::JustifiedInapplicable
                }
            } && observation.shared_invariant_routed
                && observation.capability_evidence_complete
                && observation.drift == ObservableAuthorityDrift::None;
            prop_assert_eq!(
                admit_authority_translation(&observation).is_ok(),
                model_accepts
            );
            Ok(())
        })
        .unwrap();
}

#[test]
fn property_13_harness_and_clients_bounded() {
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 128,
        ..ProptestConfig::default()
    });
    runner
        .run(&(0_u8..9, any::<usize>()), |(mutation, index)| {
            let mut observation = HarnessAndClientBoundaryObservation {
                subject_checks: required_common_harness_checks(),
                harness_self_executed: false,
                harness_claims_are_mapped: true,
                standalone_clients: required_standalone_client_cases(),
                external_workspaces: true,
                immutable_sdk_dependencies: true,
                repository_path_dependency: false,
                foreign_suite_runs: 0,
            };
            match mutation {
                0 => {}
                1 => {
                    let selected = observation
                        .subject_checks
                        .iter()
                        .nth(index % observation.subject_checks.len())
                        .copied()
                        .unwrap();
                    observation.subject_checks.remove(&selected);
                }
                2 => observation.harness_self_executed = true,
                3 => observation.harness_claims_are_mapped = false,
                4 => {
                    let selected = observation
                        .standalone_clients
                        .iter()
                        .nth(index % observation.standalone_clients.len())
                        .copied()
                        .unwrap();
                    observation.standalone_clients.remove(&selected);
                }
                5 => observation.external_workspaces = false,
                6 => observation.immutable_sdk_dependencies = false,
                7 => observation.repository_path_dependency = true,
                _ => observation.foreign_suite_runs = 1,
            }
            prop_assert_eq!(
                admit_harness_and_client_boundary(&observation).is_ok(),
                mutation == 0
            );
            Ok(())
        })
        .unwrap();
}

#[test]
fn property_14_module_authoring_complete_semantic_matrix() {
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 128,
        ..ProptestConfig::default()
    });
    runner
        .run(&(0_u8..6, any::<usize>()), |(mutation, index)| {
            let mut observation = ModuleSemanticMatrixObservation {
                semantics: required_module_semantics(),
                grouped_cases: required_module_authoring_cases(),
                production_dispatcher: true,
                artifact_sdk_content_only: true,
                fixture_dispatcher_used: false,
            };
            match mutation {
                0 => {}
                1 => {
                    let selected = observation
                        .semantics
                        .iter()
                        .nth(index % observation.semantics.len())
                        .copied()
                        .unwrap();
                    observation.semantics.remove(&selected);
                }
                2 => {
                    let selected = observation
                        .grouped_cases
                        .iter()
                        .nth(index % observation.grouped_cases.len())
                        .copied()
                        .unwrap();
                    observation.grouped_cases.remove(&selected);
                }
                3 => observation.production_dispatcher = false,
                4 => observation.artifact_sdk_content_only = false,
                _ => observation.fixture_dispatcher_used = true,
            }
            prop_assert_eq!(
                admit_module_semantic_matrix(&observation).is_ok(),
                mutation == 0
            );
            Ok(())
        })
        .unwrap();
}

#[test]
fn property_15_core_and_clients_use_public_generated_apis() {
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 128,
        ..ProptestConfig::default()
    });
    runner
        .run(&(0_u8..9, any::<usize>()), |(mutation, index)| {
            let mut observation = GeneratedApiBoundaryObservation {
                core_shapes: required_core_shapes(),
                client_cases: required_standalone_client_cases(),
                immutable_remote_revision: true,
                owned_generated_content_changed: true,
                authored_content_preserved: true,
                public_core_query: true,
                generated_namespaced_module_query: true,
                ambient_path_dependency: false,
            };
            match mutation {
                0 => {}
                1 => {
                    let selected = observation
                        .core_shapes
                        .iter()
                        .nth(index % observation.core_shapes.len())
                        .copied()
                        .unwrap();
                    observation.core_shapes.remove(&selected);
                }
                2 => {
                    let selected = observation
                        .client_cases
                        .iter()
                        .nth(index % observation.client_cases.len())
                        .copied()
                        .unwrap();
                    observation.client_cases.remove(&selected);
                }
                3 => observation.immutable_remote_revision = false,
                4 => observation.owned_generated_content_changed = false,
                5 => observation.authored_content_preserved = false,
                6 => observation.public_core_query = false,
                7 => observation.generated_namespaced_module_query = false,
                _ => observation.ambient_path_dependency = true,
            }
            prop_assert_eq!(
                admit_generated_api_boundary(&observation).is_ok(),
                mutation == 0
            );
            Ok(())
        })
        .unwrap();
}
