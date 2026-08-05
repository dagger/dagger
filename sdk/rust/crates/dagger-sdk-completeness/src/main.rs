//! Placeholder binary boundary for the completeness contract tool.
//!
//! F1 establishes the library contract before the CLI workflow is specified. The binary therefore
//! fails explicitly instead of exposing an accidental or partially validated command interface;
//! Task 12 will replace this boundary with the approved CLI.

use std::process::ExitCode;

fn main() -> ExitCode {
    eprintln!("dagger-sdk-completeness command wiring is not available in this build");
    ExitCode::from(2)
}
