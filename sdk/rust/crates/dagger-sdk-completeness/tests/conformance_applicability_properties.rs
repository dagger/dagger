//! Independent set-join and truth-table checks for complete applicability admission.

use std::collections::BTreeMap;
use std::sync::OnceLock;

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::Config;

const COMPLETENESS: &str = "../../completeness";

#[derive(Clone)]
struct Fixture {
    ledger: ResolvedLedger,
    reviewed: ReviewedConformanceScope,
    input: ConformanceScopeInput,
    same: Vec<usize>,
    idiomatic: Vec<usize>,
    engine: Vec<usize>,
    foreign_routed: Vec<usize>,
}

fn fixture() -> &'static Fixture {
    static FIXTURE: OnceLock<Fixture> = OnceLock::new();
    FIXTURE.get_or_init(|| {
        let read = |path: &str| {
            std::fs::read(format!(
                "{}/{COMPLETENESS}/{path}",
                env!("CARGO_MANIFEST_DIR")
            ))
            .unwrap()
        };
        let ledger = decode_canonical(&read("artifacts/ledger.json")).unwrap();
        let reviewed = decode_canonical(&read("conformance-scope.json")).unwrap();
        let input: ConformanceScopeInput =
            decode_canonical(&read("conformance-applicability.json")).unwrap();
        let indexes = |disposition: ApplicabilityDisposition| {
            input
                .existing_records
                .iter()
                .enumerate()
                .filter_map(|(index, record)| (record.disposition == disposition).then_some(index))
                .collect::<Vec<_>>()
        };
        let foreign_routed = input
            .existing_records
            .iter()
            .enumerate()
            .filter_map(|(index, record)| {
                (record.disposition == ApplicabilityDisposition::ForeignSdkNoRustObligation
                    && !record.assertion_ids.is_empty())
                .then_some(index)
            })
            .collect();
        Fixture {
            ledger,
            reviewed,
            same: indexes(ApplicabilityDisposition::RustObservableSameMechanism),
            idiomatic: indexes(ApplicabilityDisposition::RustObservableIdiomatic),
            engine: indexes(ApplicabilityDisposition::EngineOwnedNoRustObligation),
            foreign_routed,
            input,
        }
    })
}

fn reference_accepts(
    ledger: &ResolvedLedger,
    reviewed: &ReviewedConformanceScope,
    input: &ConformanceScopeInput,
) -> bool {
    if input.format_version != reviewed.format_version
        || input.target_digest != reviewed.target_digest
        || input.existing_scope_digest != reviewed.existing_scope_digest
    {
        return false;
    }
    let current = ledger
        .capabilities
        .values()
        .filter(|row| {
            row.owner_feature == Some(FeatureId::Feature8)
                && matches!(
                    row.authority_id.as_str(),
                    "go-client" | "go-integration-tests"
                )
        })
        .map(|row| (row.capability_id.clone(), row))
        .collect::<BTreeMap<_, _>>();
    let expected = reviewed
        .existing_records
        .iter()
        .map(|row| (row.capability_id.clone(), row))
        .collect::<BTreeMap<_, _>>();
    if current.len() != 1_081 || expected.len() != 1_081 || current.keys().ne(expected.keys()) {
        return false;
    }
    if expected.iter().any(|(capability_id, reviewed_row)| {
        let row = current[capability_id];
        row.authority_id != reviewed_row.authority_id
            || row.source_anchors != reviewed_row.source_anchors
            || row.capability_fingerprint != reviewed_row.source_fingerprint
            || row.status != reviewed_row.status
    }) {
        return false;
    }

    let records = input
        .existing_records
        .iter()
        .map(|record| (record.capability_id.clone(), record))
        .collect::<BTreeMap<_, _>>();
    if records.len() != input.existing_records.len()
        || records.len() != expected.len()
        || records.keys().ne(expected.keys())
    {
        return false;
    }
    if records.iter().any(|(capability_id, record)| {
        let row = current[capability_id];
        record.source_fingerprint != row.capability_fingerprint
            || reference_anchor(row).as_ref() != Some(&record.authority_anchor)
            || !reference_decision_is_valid(row, record)
    }) {
        return false;
    }

    let policies = input
        .policy_capabilities
        .iter()
        .map(|policy| (policy.capability_id.clone(), policy))
        .collect::<BTreeMap<_, _>>();
    let reviewed_policies = reviewed_policy_capabilities();
    let expected_policies = reviewed_policies
        .iter()
        .map(|policy| (policy.capability_id.clone(), policy))
        .collect::<BTreeMap<_, _>>();
    policies.len() == input.policy_capabilities.len() && policies == expected_policies
}

fn reference_anchor(row: &CapabilityRecord) -> Option<AuthorityAnchor> {
    let [source] = row.source_anchors.as_slice() else {
        return None;
    };
    Some(AuthorityAnchor {
        repository: source.repository.clone(),
        revision: source.revision.clone(),
        path: source.path.clone(),
        locator: source.locator.clone(),
        source_item_kind: SourceItemKind::new(row.capability_kind.as_str()).ok()?,
    })
}

fn reference_decision_is_valid(row: &CapabilityRecord, record: &ApplicabilityRecord) -> bool {
    let blocking = matches!(row.status, Status::Missing | Status::Partial);
    match (&record.disposition, &record.decision_evidence) {
        (ApplicabilityDisposition::RustObservableSameMechanism, None) => {
            blocking
                && record.terminal_policy == row.status
                && !record.assertion_ids.is_empty()
                && !record.case_ids.is_empty()
        }
        (
            ApplicabilityDisposition::RustObservableIdiomatic,
            Some(ApplicabilityDecision::IdiomaticEquivalence {
                observable_contract,
                rust_mechanism,
            }),
        ) => {
            blocking
                && record.terminal_policy == row.status
                && observable_contract != rust_mechanism
                && observable_contract.as_str().starts_with("authority/")
                && rust_mechanism.as_str().starts_with("public-rust-sdk/")
                && !record.assertion_ids.is_empty()
                && !record.case_ids.is_empty()
        }
        (
            ApplicabilityDisposition::EngineOwnedNoRustObligation,
            Some(ApplicabilityDecision::EngineOwned {
                no_rust_input,
                no_rust_output,
                no_rust_lifecycle,
                no_rust_compatibility,
            }),
        ) => {
            blocking
                && record.terminal_policy == Status::Inapplicable
                && record.assertion_ids.is_empty()
                && record.case_ids.is_empty()
                && *no_rust_input
                && *no_rust_output
                && *no_rust_lifecycle
                && *no_rust_compatibility
        }
        (
            ApplicabilityDisposition::ForeignSdkNoRustObligation,
            Some(ApplicabilityDecision::ForeignSdk {
                foreign_mechanism,
                shared_assertion_ids,
            }),
        ) => {
            blocking
                && record.terminal_policy == Status::Inapplicable
                && record.case_ids.is_empty()
                && shared_assertion_ids == &record.assertion_ids
                && foreign_mechanism.as_str().starts_with("authority/")
                && foreign_mechanism.as_str().split('/').count() >= 3
        }
        _ => false,
    }
}

fn mutate_scope(
    case: u8,
    index: usize,
) -> (
    ResolvedLedger,
    ReviewedConformanceScope,
    ConformanceScopeInput,
) {
    let fixture = fixture();
    let mut ledger = fixture.ledger.clone();
    let mut reviewed = fixture.reviewed.clone();
    let mut input = fixture.input.clone();
    let record_index = index % input.existing_records.len();
    let capability_id = input.existing_records[record_index].capability_id.clone();
    match case {
        0 => {
            let record_len = input.existing_records.len();
            input.existing_records.rotate_left(index % record_len);
            let policy_len = input.policy_capabilities.len();
            input.policy_capabilities.rotate_left(index % policy_len);
        }
        1 => {
            ledger.capabilities.remove(&capability_id);
        }
        2 => {
            ledger
                .capabilities
                .get_mut(&capability_id)
                .unwrap()
                .capability_fingerprint = Digest::sha256("changed fingerprint");
        }
        3 => {
            ledger.capabilities.get_mut(&capability_id).unwrap().status = Status::Implemented;
        }
        4 => {
            input.existing_records.remove(record_index);
        }
        5 => {
            input
                .existing_records
                .push(input.existing_records[record_index].clone());
        }
        6 => {
            input.existing_records[record_index].capability_id =
                CapabilityId::new("behavior/conformance/outside-reviewed-scope").unwrap();
        }
        7 => {
            input.policy_capabilities.pop();
        }
        8 => {
            input
                .policy_capabilities
                .push(input.policy_capabilities[0].clone());
        }
        9 => {
            input.policy_capabilities[0].fingerprint = Digest::sha256("changed policy");
        }
        10 => {
            input.existing_scope_digest = Digest::sha256("stale input scope");
        }
        _ => {
            reviewed.existing_scope_digest = Digest::sha256("stale reviewed scope");
        }
    }
    (ledger, reviewed, input)
}

fn mutate_applicability(case: u8, index: usize) -> ConformanceScopeInput {
    let fixture = fixture();
    let mut input = fixture.input.clone();
    let same = fixture.same[index % fixture.same.len()];
    let idiomatic = fixture.idiomatic[index % fixture.idiomatic.len()];
    let engine = fixture.engine[index % fixture.engine.len()];
    let foreign = fixture.foreign_routed[index % fixture.foreign_routed.len()];
    match case {
        0 => {
            let len = input.existing_records.len();
            input.existing_records.rotate_left(index % len);
        }
        1 => input.existing_records[same].source_fingerprint = Digest::sha256("stale record"),
        2 => {
            input.existing_records[same].authority_anchor =
                input.existing_records[idiomatic].authority_anchor.clone();
        }
        3 => input.existing_records[same].assertion_ids = CanonicalSet::default(),
        4 => input.existing_records[same].case_ids = CanonicalSet::default(),
        5 => input.existing_records[same].terminal_policy = Status::Implemented,
        6 => input.existing_records[idiomatic].decision_evidence = None,
        7 => {
            let Some(ApplicabilityDecision::IdiomaticEquivalence {
                observable_contract,
                rust_mechanism,
            }) = input.existing_records[idiomatic].decision_evidence.as_mut()
            else {
                unreachable!()
            };
            *rust_mechanism = observable_contract.clone();
        }
        8 => {
            let Some(ApplicabilityDecision::EngineOwned { no_rust_output, .. }) =
                input.existing_records[engine].decision_evidence.as_mut()
            else {
                unreachable!()
            };
            *no_rust_output = false;
        }
        9 => {
            input.existing_records[engine].assertion_ids =
                CanonicalSet::new([AssertionId::new("assertion/invalid-engine-effect").unwrap()]);
        }
        10 => {
            let Some(ApplicabilityDecision::ForeignSdk {
                shared_assertion_ids,
                ..
            }) = input.existing_records[foreign].decision_evidence.as_mut()
            else {
                unreachable!()
            };
            *shared_assertion_ids = CanonicalSet::default();
        }
        11 => {
            input.existing_records[foreign].case_ids =
                CanonicalSet::new([SignoffCaseId::new("case/invalid-foreign-route").unwrap()]);
        }
        12 => input.existing_records[foreign].terminal_policy = Status::Missing,
        13 => {
            input
                .existing_records
                .push(input.existing_records[same].clone());
        }
        _ => {
            input.existing_records.remove(same);
        }
    }
    input
}

proptest! {
    #![proptest_config(Config::with_cases(256))]

    #[test]
    fn property_01_existing_and_policy_scope_exact(case in 0_u8..12, index in any::<usize>()) {
        let (ledger, reviewed, input) = mutate_scope(case, index);
        let expected = reference_accepts(&ledger, &reviewed, &input);
        let observed = derive_conformance_scope(&ledger, &reviewed, input).is_ok();
        prop_assert_eq!(observed, expected);
    }

    #[test]
    fn property_02_applicability_total_local_evidence_gated(case in 0_u8..15, index in any::<usize>()) {
        let fixture = fixture();
        let input = mutate_applicability(case, index);
        let expected = reference_accepts(&fixture.ledger, &fixture.reviewed, &input);
        let observed = derive_conformance_scope(&fixture.ledger, &fixture.reviewed, input).is_ok();
        prop_assert_eq!(observed, expected);
    }
}
