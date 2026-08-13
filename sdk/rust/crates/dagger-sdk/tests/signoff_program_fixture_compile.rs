//! Compile-only coverage for the exact-engine public Rust fixture.
//!
//! The fixture is kept outside the publishable crate and mounted only by the sign-off adapter.
//! Including it here makes every public generated API and typed-error use part of ordinary locked
//! Rust compilation without starting an engine or copying the fixture into package contents.

#[allow(dead_code)]
mod exact_engine_program {
    include!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../../toolchains/rust-sdk-dev/testdata/core_conformance.rs"
    ));
}

#[test]
fn exact_engine_program_is_compiled_from_its_single_checked_source() {
    let source = include_str!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../../toolchains/rust-sdk-dev/testdata/core_conformance.rs"
    ));
    assert!(source.contains("QueryError::Exec { error, .. }"));
    assert!(source.contains("QueryGitOpts::default().with_keep_git_dir(false)"));
}
