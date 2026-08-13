//! Checked external-provenance registry renderer.
//!
//! The output path and registry vocabulary are fixed so refresh cannot widen into arbitrary file
//! publication or accept caller-authored provenance.

use std::fs;
use std::path::Path;
use std::process::ExitCode;

use clap::{Arg, ArgAction, Command};
use dagger_sdk_completeness::{
    canonical_bytes, compile_external_provenance_registry, reviewed_external_provenance_input,
};

const OUTPUT: &str = "../../completeness/security-provenance.json";

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            eprintln!("{message}");
            ExitCode::from(1)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = Command::new("dagger-conformance-security-policy")
        .about("Render or check the reviewed Rust SDK external-provenance registry")
        .arg(
            Arg::new("update")
                .long("update")
                .conflicts_with("check")
                .action(ArgAction::SetTrue),
        )
        .arg(
            Arg::new("check")
                .long("check")
                .conflicts_with("update")
                .action(ArgAction::SetTrue),
        )
        .get_matches();
    let registry = compile_external_provenance_registry(reviewed_external_provenance_input())
        .map_err(|_| "reviewed provenance registry did not pass its own policy")?;
    let bytes = canonical_bytes(&registry).map_err(|_| "could not encode provenance registry")?;
    let output = Path::new(env!("CARGO_MANIFEST_DIR")).join(OUTPUT);
    if matches.get_flag("update") {
        fs::write(output, bytes).map_err(|_| "could not update checked provenance registry")
    } else {
        let checked = fs::read(output).map_err(|_| "could not read checked provenance registry")?;
        if checked == bytes {
            Ok(())
        } else {
            Err("checked provenance registry is stale")
        }
    }
}
