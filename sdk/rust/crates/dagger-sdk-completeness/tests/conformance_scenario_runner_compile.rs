//! Compile guard joining the reviewed registry to its exact Rust runner source.
//!
//! Keeping this verification fixture in the completeness package prevents test-only sign-off
//! machinery from changing the product SDK digest while still compiling every registered public
//! API call during ordinary workspace tests.

#![allow(dead_code)]

#[path = "../../../../../toolchains/rust-sdk-dev/testdata/scenario_conformance.rs"]
mod scenarios;

#[test]
fn checked_registry_equals_the_compiled_runner_inventory() {
    let registry: serde_json::Value = serde_json::from_slice(include_bytes!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../completeness/conformance-scenario-realizations.json"
    )))
    .unwrap();
    let registered = registry["registrations"]
        .as_array()
        .unwrap()
        .iter()
        .map(|registration| {
            registration["realization"]["realization_id"]
                .as_str()
                .unwrap()
        })
        .collect::<Vec<_>>();
    assert_eq!(registered, scenarios::registered_realization_ids());
}
