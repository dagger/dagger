//! Bounded compiler contract for the standalone generated-client surface.

#![cfg(feature = "gen")]

#[test]
fn generated_client_compile_contract() {
    let cases = trybuild::TestCases::new();
    cases.pass("tests/generated-ui/pass/prelude.rs");
    cases.pass("tests/generated-ui/pass/explicit_trait.rs");
    cases.compile_fail("tests/generated-ui/fail/missing_trait.rs");
    cases.compile_fail("tests/generated-ui/fail/private_constructor.rs");
    cases.compile_fail("tests/generated-ui/fail/private_generated_index.rs");
    cases.compile_fail("tests/generated-ui/fail/private_options.rs");
    cases.compile_fail("tests/generated-ui/fail/private_runtime_state.rs");
    cases.compile_fail("tests/generated-ui/fail/wrong_typed_id.rs");
    cases.compile_fail("tests/generated-ui/fail/raw_id_resolver.rs");
    cases.compile_fail("tests/generated-ui/fail/sealed_core_loadable.rs");
}
