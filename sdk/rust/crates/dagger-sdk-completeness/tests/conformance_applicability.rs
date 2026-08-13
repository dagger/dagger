//! Fixed admission, grouping, audit, and definitive-client applicability checks.

use std::collections::BTreeSet;

use dagger_sdk_completeness::*;

const COMPLETENESS: &str = "../../completeness";

fn artifact(path: &str) -> Vec<u8> {
    std::fs::read(format!(
        "{}/{COMPLETENESS}/{path}",
        env!("CARGO_MANIFEST_DIR")
    ))
    .unwrap()
}

fn checked() -> (
    ResolvedLedger,
    ReviewedConformanceScope,
    ApplicabilityReviewInput,
    ConformanceScopeInput,
) {
    (
        decode_canonical(&artifact("artifacts/ledger.json")).unwrap(),
        decode_canonical(&artifact("conformance-scope.json")).unwrap(),
        decode_canonical(&artifact("conformance-applicability-review.json")).unwrap(),
        decode_canonical(&artifact("conformance-applicability.json")).unwrap(),
    )
}

#[test]
fn exact_groups_expand_to_the_checked_complete_artifact_and_audit() {
    let (ledger, reviewed, review, input) = checked();
    let compiled = compile_applicability_review(&ledger, &reviewed, &review).unwrap();
    let checked_audit: ApplicabilityAudit =
        decode_canonical(&artifact("conformance-applicability-audit.json")).unwrap();
    assert_eq!(compiled.input, input);
    assert_eq!(compiled.audit, checked_audit);
    assert_eq!(compiled.scope.existing_records().len(), 1_081);
    assert_eq!(compiled.scope.policy_capabilities().len(), 21);
    assert_eq!(compiled.audit.current_blocker_count, 1_081);
    assert_eq!(compiled.audit.projected_terminal_blocker_count, 621);
    assert_eq!(compiled.audit.justified_inapplicable_count, 460);

    let grouped_ids = review
        .groups
        .iter()
        .flat_map(|group| group.capability_ids.iter().cloned())
        .collect::<Vec<_>>();
    assert_eq!(grouped_ids.len(), 1_081);
    assert_eq!(grouped_ids.iter().collect::<BTreeSet<_>>().len(), 1_081);
    let text = String::from_utf8(artifact("conformance-applicability-review.json")).unwrap();
    for forbidden in [
        "\"glob\":",
        "\"selector\":",
        "\"source_path\":",
        "\"predicate\":",
        "\"default\":",
    ] {
        assert!(!text.contains(forbidden), "review contains {forbidden}");
    }
}

#[test]
fn grouping_rejects_overlap_and_omission_but_supports_explicit_shared_routes() {
    let (ledger, reviewed, review, _) = checked();
    let capability_id = review.groups[0].capability_ids[0].clone();

    let mut overlap = review.clone();
    overlap.groups[1].capability_ids = CanonicalSet::new(
        overlap.groups[1]
            .capability_ids
            .iter()
            .cloned()
            .chain([capability_id.clone()]),
    );
    let errors = compile_applicability_review(&ledger, &reviewed, &overlap).unwrap_err();
    assert!(errors.as_slice().iter().any(|error| {
        error.code == ConformanceDiagnosticCode::ApplicabilityRecordInvalid
            && error.coordinate.capability_id.as_ref() == Some(&capability_id)
    }));

    let mut omitted = review.clone();
    omitted.groups[0].capability_ids = CanonicalSet::default();
    assert!(compile_applicability_review(&ledger, &reviewed, &omitted).is_err());

    let mut shared = review;
    let group = shared
        .groups
        .iter_mut()
        .find(|group| {
            group.capability_ids.len() > 1
                && matches!(group.decision, ApplicabilityGroupDecision::SameMechanism)
        })
        .unwrap();
    let shared_assertion = AssertionId::new("assertion/reviewed/shared-observable").unwrap();
    group.routing = ApplicabilityGroupRouting::Shared {
        assertion_ids: CanonicalSet::new([shared_assertion.clone()]),
        case_ids: CanonicalSet::new([
            SignoffCaseId::new("case/reviewed/shared-observable").unwrap()
        ]),
    };
    let expected_count = u32::try_from(group.capability_ids.len()).unwrap();
    let compiled = compile_applicability_review(&ledger, &reviewed, &shared).unwrap();
    assert_eq!(
        compiled.audit.assertion_sharing.get(&shared_assertion),
        Some(&expected_count)
    );
}

#[test]
fn all_closed_dispositions_have_compatible_local_routes_and_reverse_indexes() {
    let (ledger, reviewed, _, input) = checked();
    let scope = derive_conformance_scope(&ledger, &reviewed, input).unwrap();
    let mut dispositions = BTreeSet::new();
    for record in scope.existing_records().values() {
        dispositions.insert(record.disposition.clone());
        for assertion_id in record.assertion_ids.iter() {
            assert!(
                scope
                    .assertion_capabilities()
                    .get(assertion_id)
                    .unwrap()
                    .contains(&record.capability_id)
            );
        }
        for case_id in record.case_ids.iter() {
            assert!(
                scope
                    .case_capabilities()
                    .get(case_id)
                    .unwrap()
                    .contains(&record.capability_id)
            );
        }
    }
    assert_eq!(
        dispositions,
        BTreeSet::from([
            ApplicabilityDisposition::RustObservableSameMechanism,
            ApplicabilityDisposition::RustObservableIdiomatic,
            ApplicabilityDisposition::EngineOwnedNoRustObligation,
            ApplicabilityDisposition::ForeignSdkNoRustObligation,
        ])
    );
}

#[test]
fn exact_anchor_fingerprint_and_decision_defects_fail_closed() {
    let (ledger, reviewed, _, input) = checked();
    let same = input
        .existing_records
        .iter()
        .position(|record| {
            record.disposition == ApplicabilityDisposition::RustObservableSameMechanism
        })
        .unwrap();
    let idiomatic = input
        .existing_records
        .iter()
        .position(|record| record.disposition == ApplicabilityDisposition::RustObservableIdiomatic)
        .unwrap();
    let engine = input
        .existing_records
        .iter()
        .position(|record| {
            record.disposition == ApplicabilityDisposition::EngineOwnedNoRustObligation
        })
        .unwrap();
    let foreign = input
        .existing_records
        .iter()
        .position(|record| {
            record.disposition == ApplicabilityDisposition::ForeignSdkNoRustObligation
                && !record.assertion_ids.is_empty()
        })
        .unwrap();

    let mut mutations = Vec::new();
    let mut stale = input.clone();
    stale.existing_records[same].source_fingerprint = Digest::sha256("stale");
    mutations.push((stale, ConformanceDiagnosticCode::ApplicabilityRecordInvalid));

    let mut no_case = input.clone();
    no_case.existing_records[same].case_ids = CanonicalSet::default();
    mutations.push((
        no_case,
        ConformanceDiagnosticCode::ApplicabilityDecisionInvalid,
    ));

    let mut no_equivalence = input.clone();
    no_equivalence.existing_records[idiomatic].decision_evidence = None;
    mutations.push((
        no_equivalence,
        ConformanceDiagnosticCode::ApplicabilityDecisionInvalid,
    ));

    let mut rust_effect = input.clone();
    let Some(ApplicabilityDecision::EngineOwned { no_rust_input, .. }) = rust_effect
        .existing_records[engine]
        .decision_evidence
        .as_mut()
    else {
        unreachable!()
    };
    *no_rust_input = false;
    mutations.push((
        rust_effect,
        ConformanceDiagnosticCode::ApplicabilityDecisionInvalid,
    ));

    let mut unrouted = input.clone();
    let Some(ApplicabilityDecision::ForeignSdk {
        shared_assertion_ids,
        ..
    }) = unrouted.existing_records[foreign]
        .decision_evidence
        .as_mut()
    else {
        unreachable!()
    };
    *shared_assertion_ids = CanonicalSet::default();
    mutations.push((
        unrouted,
        ConformanceDiagnosticCode::ApplicabilityDecisionInvalid,
    ));

    for (mutation, code) in mutations {
        let errors = derive_conformance_scope(&ledger, &reviewed, mutation).unwrap_err();
        assert!(errors.as_slice().iter().any(|error| error.code == code));
    }
}

#[test]
fn foreign_language_mechanisms_are_exact_and_shared_observations_remain_routed() {
    let (_, _, _, input) = checked();
    let paths = BTreeSet::from([
        "core/integration/module_typescript_test.go",
        "core/integration/module_python_test.go",
        "core/integration/module_go_test.go",
        "core/integration/module_dang_test.go",
        "core/integration/module_java_test.go",
        "core/integration/module_php_test.go",
        "core/integration/module_elixir_test.go",
        "core/integration/module_builtin_dang_test.go",
    ]);
    let records = input
        .existing_records
        .iter()
        .filter(|record| paths.contains(record.authority_anchor.path.as_str()))
        .collect::<Vec<_>>();
    assert_eq!(records.len(), 387);
    assert!(records.iter().all(|record| {
        record.disposition == ApplicabilityDisposition::ForeignSdkNoRustObligation
            && matches!(
                record.decision_evidence,
                Some(ApplicabilityDecision::ForeignSdk { .. })
            )
    }));
    assert!(
        records
            .iter()
            .filter(|record| !record.assertion_ids.is_empty())
            .all(|record| record.case_ids.is_empty())
    );
}

#[test]
fn benchmark_harness_is_neutral_but_embedded_correctness_remains_rust_observable() {
    let (_, _, _, input) = checked();
    let records = input
        .existing_records
        .iter()
        .filter(|record| {
            record.authority_anchor.path.as_str() == "core/integration/module_benchmark_test.go"
        })
        .collect::<Vec<_>>();
    assert_eq!(records.len(), 5);
    let harness = records
        .iter()
        .find(|record| record.authority_anchor.source_item_kind.as_str() == "go-function")
        .unwrap();
    assert_eq!(
        harness.disposition,
        ApplicabilityDisposition::ForeignSdkNoRustObligation
    );
    assert!(harness.assertion_ids.is_empty());
    assert!(
        records
            .iter()
            .filter(|record| record.authority_anchor.source_item_kind.as_str() == "go-method")
            .all(|record| {
                record.disposition == ApplicabilityDisposition::RustObservableSameMechanism
                    && record.assertion_ids.len() == 1
                    && record.case_ids.len() == 1
            })
    );
}

#[test]
fn definitive_go_client_behaviours_map_to_nine_idiomatic_public_rust_contracts() {
    let (ledger, _, _, input) = checked();
    let expected = BTreeSet::from([
        "authority/go-client/container",
        "authority/go-client/container-mutation",
        "authority/go-client/directory",
        "authority/go-client/exec-error-empty-output",
        "authority/go-client/exec-error-output-fields",
        "authority/go-client/git",
        "authority/go-client/list",
        "authority/go-client/non-exec-error-separation",
        "authority/go-client/typed-exec-error",
    ]);
    let records = input
        .existing_records
        .iter()
        .filter(|record| {
            record.authority_anchor.repository.as_str() == "github.com/dagger/dagger-go-sdk"
        })
        .collect::<Vec<_>>();
    assert_eq!(records.len(), 9);
    let observed = records
        .iter()
        .map(|record| {
            let current = ledger.capabilities.get(&record.capability_id).unwrap();
            assert_eq!(record.source_fingerprint, current.capability_fingerprint);
            assert_eq!(record.authority_anchor.path.as_str(), "client_test.go");
            assert_eq!(
                record.disposition,
                ApplicabilityDisposition::RustObservableIdiomatic
            );
            assert_eq!(record.assertion_ids.len(), 1);
            assert_eq!(record.case_ids.len(), 1);
            let Some(ApplicabilityDecision::IdiomaticEquivalence {
                observable_contract,
                rust_mechanism,
            }) = &record.decision_evidence
            else {
                panic!("definitive client row lacks idiomatic evidence")
            };
            assert!(rust_mechanism.as_str().starts_with("public-rust-sdk/"));
            observable_contract.as_str()
        })
        .collect::<BTreeSet<_>>();
    assert_eq!(observed, expected);
}

#[test]
fn applicability_sources_keep_review_policy_out_of_production_comments() {
    for source in [
        include_str!("../src/conformance/applicability.rs"),
        include_str!("../src/conformance/applicability_review.rs"),
        include_str!("../src/conformance/assertion.rs"),
        include_str!("../src/conformance/case_catalog.rs"),
        include_str!("../src/conformance/closure.rs"),
        include_str!("../src/conformance/planning.rs"),
        include_str!("../src/bin/dagger-conformance-applicability.rs"),
        include_str!("../src/bin/dagger-conformance-catalog.rs"),
    ] {
        for forbidden in ["// Feature:", "// Task ", "// Property "] {
            assert!(!source.contains(forbidden), "source contains {forbidden}");
        }
    }
}
