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
        .collect::<std::collections::BTreeSet<_>>();
    let compiled = scenarios::registered_realization_ids()
        .iter()
        .copied()
        .collect::<std::collections::BTreeSet<_>>();
    assert!(registered.is_subset(&compiled));
    assert_eq!(compiled.len() - registered.len(), 14);
    for fixed in [
        "realization/common-harness",
        "realization/module-concurrency-cancellation",
        "realization/module-common-harness",
        "realization/module-constructor-state",
        "realization/module-execution-shapes",
        "realization/module-handles-context",
        "realization/module-negative-dispatch",
        "realization/module-packaged-self-consumer",
        "realization/module-registration",
        "realization/module-types",
        "realization/packaged-runtime",
        "realization/shared-cli",
        "realization/stable-connector",
        "realization/standalone-clients",
    ] {
        assert!(compiled.contains(fixed));
    }
}

#[test]
fn stable_connector_fixture_uses_isolated_typed_production_observations() {
    let source = include_str!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../../toolchains/rust-sdk-dev/testdata/scenario_conformance.rs"
    ));
    for required in [
        ".env_remove(\"DAGGER_SESSION_PORT\")",
        ".env_remove(\"DAGGER_SESSION_TOKEN\")",
        ".env_remove(\"_EXPERIMENTAL_DAGGER_CLI_BIN\")",
        "SignoffConnectorEvent::ManifestAvailable",
        "SignoffConnectorEvent::ManifestUnavailable",
        "SignoffConnectorEvent::ArchiveChecksumVerified",
        "SignoffConnectorEvent::AuthenticatedLoopbackConstructed",
        "SignoffConnectorEvent::AuthenticatedQuerySucceeded",
        "SignoffConnectorEvent::ChildReaped",
    ] {
        assert!(
            source.contains(required),
            "stable connector omitted {required}"
        );
    }
    assert!(!source.contains("stable-default-connector-reached-exact-target"));
}
