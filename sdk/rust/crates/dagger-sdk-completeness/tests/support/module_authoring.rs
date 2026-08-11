//! Valid-first module-authoring generators and scoped execution defaults.

use std::collections::BTreeSet;

use dagger_codegen::module::ModuleSourcePath;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

/// Default case count for pure compiler, model, and package properties.
pub const PURE_CASES: u32 = 256;
/// Default case count for bounded filesystem and concurrency properties.
pub const FILESYSTEM_CONCURRENCY_CASES: u32 = 128;

/// Creates the pure-property runner configuration with stable failure persistence.
pub fn pure_config() -> Config {
    config(PURE_CASES)
}

/// Creates the bounded filesystem/concurrency runner configuration.
pub fn filesystem_concurrency_config() -> Config {
    config(FILESYSTEM_CONCURRENCY_CASES)
}

/// Generates a valid normalized package-relative Rust source path first.
pub fn source_path_strategy() -> BoxedStrategy<ModuleSourcePath> {
    prop::collection::vec("[a-z][a-z0-9_]{0,11}", 1..5)
        .prop_map(|mut components| {
            let file = components
                .pop()
                .expect("strategy always generates one component");
            let mut value = components.join("/");
            if !value.is_empty() {
                value.push('/');
            }
            value.push_str(&file);
            value.push_str(".rs");
            ModuleSourcePath::new(value).expect("strategy constructs normalized paths")
        })
        .boxed()
}

/// Generates bounded valid document sets and scheduler choices for later I/O layers.
pub fn filesystem_concurrency_shape_strategy()
-> BoxedStrategy<(BTreeSet<ModuleSourcePath>, Vec<u8>)> {
    (
        prop::collection::btree_set(source_path_strategy(), 1..16),
        prop::collection::vec(any::<u8>(), 0..32),
    )
        .boxed()
}

/// Generates dependency aliases accepted by Rust 2024 paths.
pub fn dependency_alias_strategy() -> BoxedStrategy<String> {
    "[a-z][a-z0-9_]{0,15}".boxed()
}

fn config(cases: u32) -> Config {
    Config {
        cases,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/module-authoring.txt"
        )))),
        ..Config::default()
    }
}
