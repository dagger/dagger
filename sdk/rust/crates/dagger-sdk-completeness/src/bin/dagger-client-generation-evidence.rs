//! Canonical recorder for standalone-client implementation closure.

use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, ArgAction, Command, value_parser};
use dagger_sdk_completeness::{
    CanonicalSet, ClientGenerationClosureObservation, ClientGenerationEvidenceArtifact,
    ClientGenerationFormatVersion, admit_client_generation_closure, canonical_bytes,
    client_generation_scope_input, decode_canonical, derive_client_generation_report,
    derive_client_generation_scope, required_client_signoff_cases,
};

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            eprintln!("could not record standalone-client closure: {message}");
            ExitCode::from(2)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = Command::new("dagger-client-generation-evidence")
        .arg(
            Arg::new("observation")
                .long("observation")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("evidence-output")
                .long("evidence-output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("report-output")
                .long("report-output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(Arg::new("check").long("check").action(ArgAction::SetTrue))
        .get_matches();
    let observation_path = matches
        .get_one::<PathBuf>("observation")
        .ok_or("observation path is absent")?;
    let evidence_path = matches
        .get_one::<PathBuf>("evidence-output")
        .ok_or("evidence output path is absent")?;
    let report_path = matches
        .get_one::<PathBuf>("report-output")
        .ok_or("report output path is absent")?;

    let observation: ClientGenerationClosureObservation =
        decode_canonical(&fs::read(observation_path).map_err(|_| "observation is unavailable")?)
            .map_err(|_| "observation is not canonical")?;
    let scope = derive_client_generation_scope(
        &client_generation_scope_input(observation.target_digest.clone()),
        &observation.target_digest,
    )
    .map_err(|_| "reviewed standalone-client scope is invalid")?;
    let closure = admit_client_generation_closure(&scope, &observation)
        .map_err(|_| "observation does not close the engine-free implementation")?;
    let report = derive_client_generation_report(&scope, Some(&closure), None)
        .map_err(|_| "closure cannot produce the standalone-client report")?;
    let artifact = ClientGenerationEvidenceArtifact {
        format_version: ClientGenerationFormatVersion::current(),
        observation,
        closure,
        deferred_signoff_cases: CanonicalSet::new(required_client_signoff_cases()),
    };
    let evidence = canonical_bytes(&artifact).map_err(|_| "evidence encoding failed")?;
    let report = canonical_bytes(&report).map_err(|_| "report encoding failed")?;
    if matches.get_flag("check") {
        require_equal(evidence_path, &evidence)?;
        require_equal(report_path, &report)
    } else {
        write_atomic(evidence_path, &evidence)?;
        write_atomic(report_path, &report)
    }
}

fn require_equal(path: &Path, expected: &[u8]) -> Result<(), &'static str> {
    if fs::read(path).map_err(|_| "checked output is unavailable")? == expected {
        Ok(())
    } else {
        Err("checked output differs from the admitted canonical artifact")
    }
}

fn write_atomic(path: &Path, bytes: &[u8]) -> Result<(), &'static str> {
    let parent = path.parent().ok_or("output has no parent directory")?;
    fs::create_dir_all(parent).map_err(|_| "output directory could not be created")?;
    let file_name = path
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or("output file name is invalid")?;
    let staged = parent.join(format!(".{file_name}.tmp"));
    fs::write(&staged, bytes).map_err(|_| "staged output could not be written")?;
    fs::rename(staged, path).map_err(|_| "staged output could not be published")
}
