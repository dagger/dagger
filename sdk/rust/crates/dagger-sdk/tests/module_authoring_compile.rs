//! Compile fixtures for the public module-authoring attributes.

#[test]
fn module_authoring_attributes_compile_or_fail_at_the_authored_item() {
    let cases = trybuild::TestCases::new();
    cases.pass("tests/fixtures/module_authoring/pass/*.rs");
    cases.compile_fail("tests/fixtures/module_authoring/fail/*.rs");
}
