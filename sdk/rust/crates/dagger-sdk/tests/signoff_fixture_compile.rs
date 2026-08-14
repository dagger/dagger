//! Compile guard for the exact-target sign-off fixture.
//!
//! The fixture remains outside the publishable SDK crate because it is verification machinery,
//! not a user-facing example. Including it here still makes every ordinary Rust test build prove
//! that its public generated-client calls compile against the current SDK surface.

#![allow(dead_code)]

mod fixture {
    include!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../../toolchains/rust-sdk-dev/testdata/core_conformance.rs"
    ));
}

#[test]
fn exact_target_signoff_fixture_compiles_with_the_public_sdk() {}
