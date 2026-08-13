//! Fixed assertion, fixture, and case-catalog checks over the pinned authorities.

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config as ProptestConfig, TestRunner};

const COMPLETENESS: &str = "../../completeness";

fn artifact(path: &str) -> Vec<u8> {
    std::fs::read(format!(
        "{}/{COMPLETENESS}/{path}",
        env!("CARGO_MANIFEST_DIR")
    ))
    .unwrap()
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
        &ledger,
        &scope,
        &harness,
        &engine,
        SubjectIdentity::SourceDigest(Digest::sha256("checked Rust source fixture")),
    )
    .unwrap()
}

#[test]
fn checked_plan_contains_every_applicability_and_fixed_route() {
    let plan = checked_plan();
    assert_eq!(plan.assertions.assertions.len(), 1_047);
    assert_eq!(plan.fixtures.fixtures.len(), 1_047);
    assert_eq!(plan.cases.cases.len(), 672);
    assert_eq!(plan.assertion_catalog.assertions().len(), 1_047);
    assert_eq!(plan.fixture_registry.fixtures().len(), 1_047);
    assert_eq!(plan.case_catalog.cases().len(), 672);
    assert_eq!(required_common_harness_checks().len(), 17);
    assert_eq!(required_core_shapes().len(), 9);
    assert_eq!(required_engine_integration_cases().len(), 10);
    assert_eq!(required_module_authoring_cases().len(), 9);
    assert_eq!(required_standalone_client_cases().len(), 5);
    assert_eq!(required_go_client_behaviours().len(), 9);
    assert_eq!(required_fixed_programs().len(), 60);
}

#[test]
fn catalog_contains_no_command_or_foreign_executor_surface() {
    let plan = checked_plan();
    let bytes = canonical_bytes(&plan.cases).unwrap();
    let text = String::from_utf8(bytes).unwrap();
    for forbidden in [
        "\"command\":",
        "\"arguments\":",
        "\"working_directory\":",
        "\"executable\":",
        "executor/typescript",
        "executor/python",
        "executor/go-sdk-suite",
    ] {
        assert!(!text.contains(forbidden), "catalog contains {forbidden}");
    }
}

#[test]
fn harness_self_check_is_not_a_subject_case() {
    let plan = checked_plan();
    let text = String::from_utf8(canonical_bytes(&plan.cases).unwrap()).unwrap();
    assert!(!text.contains("init-module-renders-root-type"));
}

#[test]
fn authority_drift_is_empty_for_the_checked_assertion_input() {
    let ledger: ResolvedLedger = decode_canonical(&artifact("artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&artifact("conformance-scope.json")).unwrap();
    let applicability: ConformanceScopeInput =
        decode_canonical(&artifact("conformance-applicability.json")).unwrap();
    let scope = derive_conformance_scope(&ledger, &reviewed, applicability).unwrap();
    let plan = checked_plan();
    assert!(assertion_catalog_drift(&scope, &plan.assertions).is_empty());
}

#[test]
fn checked_audit_closure_plan_and_checkpoint_are_canonical_and_engine_free() {
    let closure_plan: ImplementationClosurePlanFixture =
        decode_canonical(&artifact("conformance-closure-plan.json")).unwrap();
    assert_eq!(closure_plan.actions, expected_closure_plan());
    assert!(!closure_plan.permits_historical_engine_signoff);
    assert!(closure_plan.requires_platform_matrix);
    assert!(closure_plan.requires_rust_security);

    let audit: serde_json::Value =
        decode_canonical(&artifact("conformance-catalog-audit.json")).unwrap();
    for key in [
        "engine_action_present",
        "executable_text_present",
        "replay_action_present",
    ] {
        assert_eq!(audit[key], false);
    }
    let _: serde_json::Value =
        decode_canonical(&artifact("evidence/conformance-catalog-checkpoint.json")).unwrap();
}

fn checked_scope() -> ConformanceScope {
    let ledger: ResolvedLedger = decode_canonical(&artifact("artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&artifact("conformance-scope.json")).unwrap();
    let applicability: ConformanceScopeInput =
        decode_canonical(&artifact("conformance-applicability.json")).unwrap();
    derive_conformance_scope(&ledger, &reviewed, applicability).unwrap()
}

fn independent_case_model(scope: &ConformanceScope, input: &CaseCatalogInput) -> bool {
    let by_id = input
        .cases
        .iter()
        .map(|case| (case.id.clone(), case))
        .collect::<std::collections::BTreeMap<_, _>>();
    let fixed = input
        .cases
        .iter()
        .filter(|case| case.family != CaseFamily::IntegrationAssertion)
        .map(|case| case.program.clone())
        .collect::<std::collections::BTreeSet<_>>();
    by_id.len() == input.cases.len()
        && fixed == required_fixed_programs()
        && scope
            .case_capabilities()
            .iter()
            .all(|(case_id, capabilities)| {
                by_id
                    .get(case_id)
                    .is_some_and(|case| &case.capability_ids == capabilities)
            })
        && input.cases.iter().all(|case| {
            !case.assertion_ids.is_empty()
                && !case.capability_ids.is_empty()
                && case.program.family() == case.family
                && ((case.retry.maximum_attempts.get() == 1 && case.retry.retryable.is_empty())
                    || (case.retry.maximum_attempts.get() > 1 && !case.retry.retryable.is_empty()))
        })
}

#[test]
fn property_05_case_catalog_closed_complete_deterministic() {
    let base = checked_plan();
    let scope = checked_scope();
    let expected_digest = base.case_catalog.digest().clone();
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 256,
        ..ProptestConfig::default()
    });

    // Every admitted graph is invariant to declaration order; every modeled defect is rejected.
    runner
        .run(&(0_u8..8, any::<usize>()), |(mutation, index)| {
            let mut assertions = base.assertions.clone();
            let mut fixtures = base.fixtures.clone();
            let mut cases = base.cases.clone();
            match mutation {
                0 => {
                    assertions.assertions.reverse();
                    fixtures.fixtures.reverse();
                    cases.cases.reverse();
                }
                1 => {
                    let case_len = cases.cases.len();
                    cases.cases.rotate_left(index % case_len);
                }
                2 => {
                    let position = index % cases.cases.len();
                    cases.cases.remove(position);
                }
                3 => {
                    let position = index % cases.cases.len();
                    cases.cases.push(cases.cases[position].clone());
                }
                4 => {
                    let position = index % fixtures.fixtures.len();
                    fixtures.fixtures[position].fixture_digest = Digest::sha256("drifted fixture");
                }
                5 => {
                    let position = index % cases.cases.len();
                    cases.cases[position].retry.maximum_attempts = NonZeroCount::new(2).unwrap();
                    cases.cases[position].retry.retryable = CanonicalSet::default();
                }
                6 => {
                    let position = index % cases.cases.len();
                    cases.cases[position].program =
                        if cases.cases[position].program == CaseProgram::StableConnector {
                            CaseProgram::CoreShape {
                                shape: CoreCaseShape::Scalar,
                            }
                        } else {
                            CaseProgram::StableConnector
                        };
                }
                7 => {
                    let applicability = assertions
                        .assertions
                        .iter()
                        .enumerate()
                        .filter(|(_, assertion)| assertion.origin == AssertionOrigin::Applicability)
                        .map(|(position, _)| position)
                        .collect::<Vec<_>>();
                    assertions
                        .assertions
                        .remove(applicability[index % applicability.len()]);
                }
                _ => unreachable!(),
            }

            let model_accepts = mutation < 2 && independent_case_model(&scope, &cases);
            let compiled = match mutation {
                0 => compile_assertion_catalog(&scope, assertions).and_then(|assertions| {
                    compile_fixture_registry(fixtures).and_then(|fixtures| {
                        compile_case_catalog(&scope, &assertions, &fixtures, cases)
                            .map(|catalog| Some(catalog.digest().clone()))
                    })
                }),
                4 => compile_fixture_registry(fixtures).map(|_| None),
                7 => compile_assertion_catalog(&scope, assertions).map(|_| None),
                _ => compile_case_catalog(
                    &scope,
                    &base.assertion_catalog,
                    &base.fixture_registry,
                    cases,
                )
                .map(|catalog| Some(catalog.digest().clone())),
            };
            prop_assert_eq!(compiled.is_ok(), model_accepts);
            if let Ok(Some(digest)) = compiled {
                prop_assert_eq!(&digest, &expected_digest);
            }
            Ok(())
        })
        .unwrap();
}
