//! Executable correctness properties for canonical artifacts and immutable target identity.
//!
//! Generators begin with a coherent contract graph, then vary representation order or introduce
//! named, independently selectable drift. This keeps each failure attributable to the invariant
//! under test instead of to unrelated malformed scaffolding.

use std::collections::BTreeSet;

use dagger_sdk_completeness::{
    DiagnosticCode, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
    validate_target,
};
use proptest::prelude::*;
use proptest::strategy::ValueTree;
use proptest::test_runner::{Config, RngSeed, TestRunner};
use serde::Deserialize;

mod support;

use support::{
    DurableModel, PROPTEST_CASES, TargetMutation, apply_target_mutations, commit_strategy,
    dagger_version_strategy, digest_strategy, durable_model_strategy,
    equivalent_contract_cases_strategy, locator_strategy, proptest_config, relative_path_strategy,
    semver_strategy, target_case_strategy, target_mutation_set_strategy, text_strategy,
};

#[test]
fn property_configuration_preserves_failures_and_runs_the_required_case_count() {
    let config = proptest_config();

    assert_eq!(config.cases, PROPTEST_CASES);
    assert!(config.failure_persistence.is_some());
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct TargetRegressionCase {
    name: String,
    mutations: BTreeSet<TargetMutation>,
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
