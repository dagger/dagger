//! External compile fixtures for the intentional stable facade.

#![cfg(feature = "gen")]

#[test]
fn stable_public_api_compile_contract() {
    // Feature: rust-sdk-core-codegen, Property 15: Typed ID compatibility is closed and all-or-nothing
    let cases = trybuild::TestCases::new();
    cases.pass("tests/ui/pass/*.rs");
    cases.compile_fail("tests/ui/fail/*.rs");
}
