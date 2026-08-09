//! Exact core-codegen ownership-correction regressions.

use std::fs;
use std::path::{Path, PathBuf};
use std::sync::OnceLock;

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const CASES: u32 = 256;

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/core-codegen-scope.txt"
        )))),
        ..Config::default()
    }
}

fn repository_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

fn read_canonical<T>(path: &Path) -> T
where
    T: serde::de::DeserializeOwned + serde::Serialize,
{
    decode_canonical(&fs::read(path).expect("checked fixture must be readable"))
        .expect("checked fixture must be canonical")
}

struct Fixtures {
    inventory: CanonicalInventory,
    checked_after: ResolvedLedger,
    reference_before: ResolvedLedger,
    contract: CoreCodegenScopeContract,
}

fn fixtures() -> &'static Fixtures {
    static FIXTURES: OnceLock<Fixtures> = OnceLock::new();
    FIXTURES.get_or_init(|| {
        let root = repository_root().join("sdk/rust/completeness");
        let inventory = read_canonical(&root.join("artifacts/inventory.json"));
        let source_items = read_canonical(&root.join("artifacts/source-items.json"));
        let classifications = read_canonical(&root.join("classifications.json"));
        let contract = read_canonical(&root.join("core-codegen-scope.json"));
        let reference_before = resolve_classifications(&inventory, &source_items, &classifications)
            .expect("authored baseline classifications must resolve");
        let checked_after = reference_after(&reference_before, &contract);
        Fixtures {
            inventory,
            checked_after,
            reference_before,
            contract,
        }
    })
}

fn reference_after(before: &ResolvedLedger, contract: &CoreCodegenScopeContract) -> ResolvedLedger {
    let mut after = before.clone();
    for correction in &contract.corrections {
        let paths = correction
            .source_paths
            .iter()
            .collect::<std::collections::BTreeSet<_>>();
        for row in after.capabilities.values_mut() {
            let explicit = correction.capability_ids.contains(&row.capability_id);
            let selected_path = row
                .source_anchors
                .iter()
                .any(|anchor| paths.contains(&anchor.path));
            if explicit || selected_path {
                row.owner_feature = Some(correction.destination.clone());
            }
        }
    }
    after
}

fn mutate(
    before: &mut ResolvedLedger,
    contract: &mut CoreCodegenScopeContract,
    mutation: u8,
    seed: u64,
) {
    let baseline_ids = before
        .capabilities
        .values()
        .filter(|row| {
            row.owner_feature == Some(FeatureId::Feature4)
                && row.authority_id.as_str() != "rust-policy"
        })
        .map(|row| row.capability_id.clone())
        .collect::<Vec<_>>();
    let selected = &baseline_ids[(seed as usize) % baseline_ids.len()];
    match mutation {
        1 => {
            before
                .capabilities
                .get_mut(selected)
                .expect("selected row must exist")
                .owner_feature = Some(FeatureId::Feature5)
        }
        2 => {
            let row = before
                .capabilities
                .get_mut(selected)
                .expect("selected row must exist");
            row.status = if row.status == Status::Partial {
                Status::Missing
            } else {
                Status::Partial
            };
        }
        3 => {
            before
                .capabilities
                .get_mut(selected)
                .expect("selected row must exist")
                .capability_fingerprint = Digest::sha256(seed.to_le_bytes());
        }
        4 => contract.corrections.swap(0, 1),
        5 => {
            let duplicate = contract.policy_capability_ids[0].clone();
            contract.policy_capability_ids.insert(1, duplicate);
        }
        6 => contract.policy_capability_ids.reverse(),
        7 => contract.corrections[1].source_paths.swap(0, 1),
        8 => contract.corrections[0].capability_ids_digest = Digest::sha256(seed.to_le_bytes()),
        9 => {
            let policy = &contract.policy_capability_ids[0];
            before
                .capabilities
                .get_mut(policy)
                .expect("policy row must exist")
                .owner_feature = Some(FeatureId::Feature9);
        }
        10 => {
            let extra = baseline_ids
                .iter()
                .find(|capability_id| {
                    !contract.corrections[0]
                        .capability_ids
                        .contains(*capability_id)
                })
                .expect("baseline must contain an uncorrected row")
                .clone();
            contract.corrections[0].capability_ids.push(extra);
        }
        11 => {
            contract.corrections[0].capability_ids.pop();
        }
        _ => {}
    }
}

// Feature: rust-sdk-core-codegen, Property 1: Ownership correction is exact and status-neutral
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_01_ownership_correction_exact_status_neutral(
        mutation in 0_u8..12,
        seed in any::<u64>(),
    ) {
        let fixtures = fixtures();
        let mut contract = fixtures.contract.clone();
        let mut before = fixtures.reference_before.clone();
        mutate(&mut before, &mut contract, mutation, seed);

        let result = apply_core_codegen_scope_correction(&fixtures.inventory, &before, &contract);
        prop_assert_eq!(result.is_ok(), mutation == 0);
        if mutation != 0 {
            return Ok(());
        }

        let transition = result.expect("unmutated reviewed transition must validate");
        prop_assert_eq!(transition.declaration.existing_capability_ids.len(), 3261);
        prop_assert_eq!(transition.declaration.policy_capability_ids.len(), 16);
        prop_assert_eq!(transition.corrected_capability_ids[&FeatureId::Feature3].len(), 6);
        prop_assert_eq!(transition.corrected_capability_ids[&FeatureId::Feature5].len(), 19);
        prop_assert_eq!(transition.corrected_capability_ids[&FeatureId::Feature6].len(), 43);
        for (capability_id, before_row) in &before.capabilities {
            let after_row = &transition.ledger.capabilities[capability_id];
            prop_assert_eq!(&before_row.status, &after_row.status);
            if !transition
                .corrected_capability_ids
                .values()
                .any(|ids| ids.contains(capability_id))
            {
                prop_assert_eq!(before_row, after_row);
            }
        }
        prop_assert_eq!(transition.ledger, fixtures.checked_after.clone());
    }
}

#[test]
fn exact_scope_contract_uses_the_reviewed_case_floors() {
    assert_eq!(CASES, 256);
    assert_eq!(dagger_codegen_test_cases(), (256, 128));
}

fn dagger_codegen_test_cases() -> (u32, u32) {
    let support = include_str!("../../dagger-codegen/tests/support/mod.rs");
    let pure = support.contains("PURE_CASES: u32 = 256");
    let filesystem = support.contains("FILESYSTEM_CASES: u32 = 128");
    (u32::from(pure) * 256, u32::from(filesystem) * 128)
}
