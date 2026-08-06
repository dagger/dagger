//! Normalizes one Dagger-produced raw sdk-sdk profile into durable evidence identities.
//!
//! Dagger owns network acquisition and process execution. This boundary revalidates the active
//! contract, exact CLI bytes, Rust subject digest, complete check set, and raw filename set before
//! emitting canonical JSON. Subject failures remain ordinary outcomes.

use std::collections::BTreeSet;
use std::fs;
use std::io::Write as _;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, Command, value_parser};
use dagger_sdk_completeness::DigestDomain;
use dagger_sdk_completeness::{
    Architecture, CheckOutcome, Digest, HarnessCheckResult, HarnessMappings, HarnessProfileResult,
    OperatingSystem, Platform, SemverVersion, TargetDescriptor, TargetDigest, canonical_bytes,
    canonical_digest, decode_canonical, derive_contract, rust_artifact_digest,
};

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(detail) => {
            eprintln!("could not normalize pinned harness profile: {detail}");
            ExitCode::from(2)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = Command::new("dagger-sdk-harness-profile")
        .arg(
            Arg::new("root")
                .long("root")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("raw")
                .long("raw")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("cli")
                .long("cli")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .get_matches();
    let root = matches.get_one::<PathBuf>("root").unwrap();
    let raw = matches.get_one::<PathBuf>("raw").unwrap();
    let cli = matches.get_one::<PathBuf>("cli").unwrap();

    let derived = derive_contract(root, true).map_err(|_| "active contract verification failed")?;
    if !derived.report.integrity_verdict {
        return Err("active contract integrity is false");
    }
    let contract = root.join("sdk/rust/completeness");
    let target: TargetDescriptor = read_canonical(&contract.join("target.json"))?;
    let mappings: HarnessMappings = read_canonical(&contract.join("harness-mappings.json"))?;
    let target_digest = TargetDigest::new(
        canonical_digest(DigestDomain::Target, &target).map_err(|_| "target digest failed")?,
    );
    let cli_digest = Digest::sha256(fs::read(cli).map_err(|_| "CLI bytes are unavailable")?);
    let verified_digest = rust_artifact_digest(root).map_err(|_| "Rust digest failed")?;
    let platform = Platform {
        operating_system: OperatingSystem::Linux,
        architecture: Architecture::Amd64,
    };
    let expected_files = mappings
        .checks
        .keys()
        .flat_map(|check| {
            ["status", "stdout", "stderr"].map(move |extension| format!("{check}.{extension}"))
        })
        .collect::<BTreeSet<_>>();
    let actual_files = fs::read_dir(raw)
        .map_err(|_| "raw directory is unavailable")?
        .map(|entry| {
            entry
                .map_err(|_| "raw directory entry is unavailable")?
                .file_name()
                .into_string()
                .map_err(|_| "raw filename is not UTF-8")
        })
        .collect::<Result<BTreeSet<_>, _>>()?;
    if actual_files != expected_files {
        return Err("raw profile does not contain exactly three files per mapped check");
    }

    let mut results = Vec::with_capacity(mappings.checks.len());
    for (check_id, mapping) in mappings.checks {
        if mapping.execution_target != target_digest
            || mapping.harness_revision != target.sdk_contract_revision
            || mapping.cli_artifact_digest != cli_digest
            || mapping.verified_artifact_digest != verified_digest
            || !mapping.platform_scope.contains(&platform)
        {
            return Err("mapping identity differs from executed profile identity");
        }
        let status = read(raw, &format!("{check_id}.status"))?;
        let status = std::str::from_utf8(&status)
            .map_err(|_| "status is not UTF-8")?
            .trim()
            .parse::<u8>()
            .map_err(|_| "status is not an unsigned process exit code")?;
        results.push(HarnessCheckResult {
            check_id: check_id.clone(),
            check_kind: mapping.check_kind,
            harness_revision: mapping.harness_revision,
            target: mapping.execution_target,
            cli_artifact_digest: cli_digest.clone(),
            verified_artifact_digest: verified_digest.clone(),
            platform: platform.clone(),
            outcome: if status == 0 {
                CheckOutcome::Passed
            } else {
                CheckOutcome::Failed
            },
            assertion: mapping.expected_outcome.assertion,
            capability_ids: mapping.capability_ids,
            stdout_digest: Digest::sha256(read(raw, &format!("{check_id}.stdout"))?),
            stderr_digest: Digest::sha256(read(raw, &format!("{check_id}.stderr"))?),
        });
    }
    let profile = HarnessProfileResult {
        format_version: SemverVersion::new("1.0.0").expect("static version is valid"),
        target: target_digest,
        harness_revision: target.sdk_contract_revision,
        cli_artifact_digest: cli_digest,
        verified_artifact_digest: verified_digest,
        platform,
        results,
    };
    std::io::stdout()
        .write_all(&canonical_bytes(&profile).map_err(|_| "profile encoding failed")?)
        .map_err(|_| "profile output failed")
}

fn read(root: &Path, name: &str) -> Result<Vec<u8>, &'static str> {
    fs::read(root.join(name)).map_err(|_| "raw result file is unavailable")
}

fn read_canonical<T>(path: &Path) -> Result<T, &'static str>
where
    T: serde::de::DeserializeOwned + serde::Serialize,
{
    decode_canonical(&fs::read(path).map_err(|_| "contract artifact is unavailable")?)
        .map_err(|_| "contract artifact is not canonical")
}
