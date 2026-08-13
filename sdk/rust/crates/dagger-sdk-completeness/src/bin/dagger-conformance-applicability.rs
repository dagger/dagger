//! Private exact-ID applicability compiler.
//!
//! The command performs no discovery. It accepts a checked ledger, checked scope, and checked
//! exact-ID review, then writes only the fully expanded canonical artifact and its neutral audit.

use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, Command, value_parser};
use dagger_sdk_completeness::{
    ApplicabilityReviewInput, ResolvedLedger, ReviewedConformanceScope, canonical_bytes,
    compile_applicability_review, decode_canonical,
};
use serde::Serialize;

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
    let matches = Command::new("dagger-conformance-applicability")
        .about("Compile the checked exact-ID Rust SDK applicability review")
        .arg(path_argument("ledger"))
        .arg(path_argument("scope"))
        .arg(path_argument("review"))
        .arg(path_argument("output"))
        .arg(path_argument("audit"))
        .get_matches();
    let ledger: ResolvedLedger = read_checked(required_path(&matches, "ledger"))?;
    let scope: ReviewedConformanceScope = read_checked(required_path(&matches, "scope"))?;
    let review: ApplicabilityReviewInput = read_checked(required_path(&matches, "review"))?;
    let compiled = compile_applicability_review(&ledger, &scope, &review).map_err(|errors| {
        for diagnostic in errors.as_slice() {
            eprintln!(
                "{} {}",
                diagnostic.code,
                diagnostic
                    .coordinate
                    .capability_id
                    .as_ref()
                    .map_or("scope", |id| id.as_str())
            );
        }
        "applicability review was rejected"
    })?;
    write_checked(required_path(&matches, "output"), &compiled.input)?;
    write_checked(required_path(&matches, "audit"), &compiled.audit)?;
    Ok(())
}

fn path_argument(name: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .required(true)
        .value_parser(value_parser!(PathBuf))
}

fn required_path<'a>(matches: &'a clap::ArgMatches, name: &str) -> &'a Path {
    matches
        .get_one::<PathBuf>(name)
        .expect("required path is present")
}

fn read_checked<T>(path: &Path) -> Result<T, &'static str>
where
    T: serde::de::DeserializeOwned + Serialize,
{
    let bytes = fs::read(path).map_err(|_| "could not read checked applicability input")?;
    decode_canonical(&bytes).map_err(|_| "applicability input is not canonical")
}

fn write_checked<T: Serialize>(path: &Path, value: &T) -> Result<(), &'static str> {
    let bytes = canonical_bytes(value).map_err(|_| "could not encode applicability output")?;
    if fs::read(path).ok().as_deref() == Some(bytes.as_slice()) {
        return Ok(());
    }
    let parent = path.parent().ok_or("applicability output has no parent")?;
    fs::create_dir_all(parent).map_err(|_| "could not create applicability output directory")?;
    let temporary = parent.join(format!(".applicability-{}.tmp", std::process::id()));
    let result = fs::write(&temporary, &bytes)
        .and_then(|()| fs::rename(&temporary, path))
        .map_err(|_| "could not publish applicability output");
    if result.is_err() {
        let _ = fs::remove_file(temporary);
    }
    result
}
