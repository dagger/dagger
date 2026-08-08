//! Compile-time evidence for required projected construction paths.

#[test]
fn required_arguments_and_input_fields_cannot_be_omitted() {
    let tests = trybuild::TestCases::new();
    tests.compile_fail("tests/ui/required-argument.rs");
    tests.compile_fail("tests/ui/required-input-field.rs");
}
