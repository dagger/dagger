//! `dagger-rust` process entry point.

use std::io::{self, Write as _};
use std::process::ExitCode;

fn main() -> ExitCode {
    match dagger_bootstrap::cli::run(std::env::args_os()) {
        Ok(outcome) => {
            let mut stdout = io::stdout().lock();
            for path in outcome.changed_paths() {
                if writeln!(stdout, "{path}").is_err() {
                    return ExitCode::FAILURE;
                }
            }
            ExitCode::SUCCESS
        }
        Err(diagnostics) => {
            let _ = writeln!(io::stderr().lock(), "{diagnostics}");
            ExitCode::FAILURE
        }
    }
}
