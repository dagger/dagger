//! Shared valid-first generators, mutations, and failure persistence for contract properties.
//!
//! The support layer exposes one coherent durable graph and named mutation sets. Properties reuse
//! those foundations so a counterexample shrinks toward the behaviour under test rather than into
//! malformed, unrelated scaffolding.

mod authority_case;
pub(crate) mod contract_case;
mod models;
mod mutations;
mod scalars;

use proptest::test_runner::{Config, FileFailurePersistence};

pub use contract_case::{durable_model_strategy, equivalent_contract_cases_strategy};
pub use models::DurableModel;
pub use mutations::{
    TargetMutation, apply_target_mutations, target_case_strategy, target_mutation_set_strategy,
};
pub use scalars::{
    commit_strategy, dagger_version_strategy, digest_strategy, locator_strategy,
    relative_path_strategy, semver_strategy, text_strategy,
};

pub const PROPTEST_CASES: u32 = 256;

pub fn proptest_config() -> Config {
    Config {
        cases: PROPTEST_CASES,
        // Keep minimized failures beside the crate and replay them before novel random cases.
        // An explicit path also works for integration tests, where SourceParallel cannot discover
        // a sibling lib.rs from the test binary's source location.
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/properties.txt"
        )))),
        ..Config::default()
    }
}
pub use authority_case::{
    AuthorityCase, AuthorityMutation, apply_authority_mutations, authority_case_strategy,
    authority_id, authority_mutation_set_strategy,
};
