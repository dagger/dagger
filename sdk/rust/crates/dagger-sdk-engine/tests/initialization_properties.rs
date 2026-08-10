//! Initialization confinement and module-SDK clone isolation properties.

mod support;

use std::collections::{BTreeMap, BTreeSet};

use dagger_sdk_engine::initialization::{InitializationInputs, plan_initialization};
use dagger_sdk_engine::{EngineDiagnosticCode, RelativeOperationPath, ToolchainSelection};
use proptest::prelude::*;
use support::fixed_model_corpus;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(128))]

    // Planning is a pure, module-confined projection. Rejecting at any pre-publication
    // phase cannot alter the caller tree because no mutation authority is returned.
    #[test]
    fn property_05_initialization_confined_failure_atomic(
        seed in any::<u8>(),
        existing in any::<bool>(),
        authored_source in any::<bool>(),
        failure_phase in 0_u8..5,
        unrelated in proptest::collection::btree_map("unrelated/[a-z]{1,8}", proptest::collection::vec(any::<u8>(), 0..32), 0..8),
    ) {
        let corpus = fixed_model_corpus(seed, true, 1);
        let module_root = RelativeOperationPath::parse(&format!("workspace/modules/module-{seed}")).unwrap();
        let manifest = existing.then(|| format!(
            "# caller-{seed}\n[package]\nname = \"module-{seed}\"\nversion = \"0.1.0\"\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\nserde = \"1\"\n"
        ));
        let toolchain = ToolchainSelection::TargetDefault { toolchain: "1.97.1".parse().unwrap() };
        let before = unrelated.clone();
        let result = plan_initialization(InitializationInputs {
            module_root: &module_root,
            package_name: &format!("module-{seed}"),
            manifest: manifest.as_deref().map(str::as_bytes),
            starter_source: authored_source.then_some(&b"pub fn caller_owned() {}\n"[..]),
            gitignore: None,
            gitattributes: None,
            dependency: &corpus.dependency,
            toolchain: &toolchain,
            dependency_resolved: failure_phase == 0,
            lockfile_present: existing,
        });
        if failure_phase == 0 {
            let plan = result.unwrap();
            let module_prefix = format!("{}/", module_root.as_str());
            prop_assert!(plan.files.keys().all(|path| path.as_str().starts_with(&module_prefix)));
            prop_assert!(plan.files.keys().all(|path| !matches!(path.as_str().rsplit('/').next(), Some("dagger.toml" | "dagger-module.toml"))));
            let starter = RelativeOperationPath::parse(&format!("{}/src/lib.rs", module_root.as_str())).unwrap();
            prop_assert_eq!(plan.files.contains_key(&starter), !authored_source);
        } else {
            prop_assert_eq!(result.unwrap_err().code, EngineDiagnosticCode::DependencyResolutionFailed);
        }
        prop_assert_eq!(unrelated, before);
    }

    // Clone schedules use independent maps and may attach only results whose identity
    // belongs to that clone. This is replayed against the concrete Go SDK module below.
    #[test]
    fn property_16_cloned_sdk_state_isolated(
        left_identity in "module-[a-z]{1,12}",
        right_identity in "module-[a-z]{1,12}",
        schedule in proptest::collection::vec((any::<bool>(), "[a-z]{1,8}", any::<u8>()), 1..32),
    ) {
        prop_assume!(left_identity != right_identity);
        let base = CloneState::default();
        let mut left = base.clone_for(left_identity.clone());
        let mut right = base.clone_for(right_identity.clone());
        for (select_left, key, value) in schedule {
            let selected = if select_left { &mut left } else { &mut right };
            selected.configuration.insert(key, value);
            selected.attach(selected.identity.clone());
        }
        prop_assert_eq!(base, CloneState::default());
        prop_assert!(left.attachments.iter().all(|identity| identity == &left_identity));
        prop_assert!(right.attachments.iter().all(|identity| identity == &right_identity));
        prop_assert_ne!(&left.configuration as *const _, &right.configuration as *const _);
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
struct CloneState {
    identity: String,
    configuration: BTreeMap<String, u8>,
    attachments: BTreeSet<String>,
}

impl CloneState {
    fn clone_for(&self, identity: String) -> Self {
        let mut cloned = self.clone();
        cloned.identity = identity;
        cloned
    }

    fn attach(&mut self, identity: String) {
        self.attachments.insert(identity);
    }
}
