//! External compile fixtures for the intentional stable facade.

#[test]
fn stable_public_api_compile_contract() {
    let cases = trybuild::TestCases::new();
    cases.pass("tests/ui/pass/*.rs");
    cases.compile_fail("tests/ui/fail/*.rs");
}
