//! Thin binary boundary for the Rust SDK completeness contract.
//!
//! Domain validation and staging live in the library. This process owns only host streams, argv,
//! and conversion of the reviewed 0/1/2 status policy into [`ExitCode`].

use dagger_sdk_completeness::{ArtifactCliBackend, run_with_backend};
use std::process::ExitCode;

fn main() -> ExitCode {
    let status = run_with_backend(
        std::env::args_os(),
        &ArtifactCliBackend,
        &mut std::io::stdout().lock(),
        &mut std::io::stderr().lock(),
    );
    ExitCode::from(status)
}
