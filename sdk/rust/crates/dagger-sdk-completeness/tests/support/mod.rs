//! Shared valid-first generators, mutations, and failure persistence for contract properties.
//!
//! The support layer exposes one coherent durable graph and named mutation sets. Properties reuse
//! those foundations so a counterexample shrinks toward the behaviour under test rather than into
//! malformed, unrelated scaffolding.

mod contract_case;
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
        // SourceParallel gives each property module a stable, reviewable corpus beside the crate
        // while still replaying minimized failures before novel random cases.
        failure_persistence: Some(Box::new(FileFailurePersistence::SourceParallel(
            "proptest-regressions",
        ))),
        ..Config::default()
    }
}
