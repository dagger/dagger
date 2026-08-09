#![deny(warnings)]
#![deny(unsafe_code)]
//! Command-line boundary for the private Rust engine operation tool.
//!
//! Operation subcommands are added with their owning implementation slices. Until
//! then, the binary intentionally exposes only version/help metadata so an engine
//! build can package and identify the tool without implying an implemented operation.

use clap::Command;

fn main() {
    let _ = Command::new("dagger-rust-engine")
        .version(env!("CARGO_PKG_VERSION"))
        .disable_help_subcommand(true)
        .get_matches();
}
