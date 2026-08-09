//! Converts focused live observations into one canonical capability-scoped evidence record.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, Command, value_parser};
use dagger_sdk_completeness::{
    CanonicalSet, CheckOutcome, CommandSpec, ConformanceCategory, ConformanceObservation,
    CoreConformanceRun, DigestDomain, EvidenceDomain, EvidenceId, ExecutableId,
    GeneratedBindingManifest, RepositoryRelativePath, TargetDescriptor, canonical_bytes,
    canonical_digest, core_conformance_evidence, decode_canonical, required_conformance_categories,
};
use serde::Deserialize;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ObservationSet {
    format_version: u32,
    target_revision: String,
    target_version: String,
    observations: Vec<RawObservation>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawObservation {
    category: ConformanceCategory,
    operation: String,
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(detail) => {
            eprintln!("could not normalize core conformance evidence: {detail}");
            ExitCode::from(2)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = Command::new("dagger-core-conformance-evidence")
        .arg(
            Arg::new("root")
                .long("root")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("observations")
                .long("observations")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("subject-digest")
                .long("subject-digest")
                .required(true),
        )
        .arg(
            Arg::new("output")
                .long("output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .get_matches();

    let root = matches.get_one::<PathBuf>("root").unwrap();
    let observations_path = matches.get_one::<PathBuf>("observations").unwrap();
    let output = matches.get_one::<PathBuf>("output").unwrap();
    let subject_revision = dagger_sdk_completeness::Digest::new(
        matches
            .get_one::<String>("subject-digest")
            .unwrap()
            .to_owned(),
    )
    .map_err(|_| "subject digest is not a canonical SHA-256 identity")?;

    let target: TargetDescriptor = read_canonical(&root.join("completeness/target.json"))?;
    let manifest: GeneratedBindingManifest =
        read_canonical(&root.join("completeness/artifacts/core-codegen-bindings.json"))?;
    let observations: ObservationSet = serde_json::from_slice(
        &fs::read(observations_path).map_err(|_| "observation file is unavailable")?,
    )
    .map_err(|_| "observation file is invalid JSON")?;

    if observations.format_version != 1
        || observations.target_revision != manifest.target_revision
        || observations.target_revision != target.dagger_revision.as_str()
        || observations.target_version != target.engine_version.to_string()
        || manifest.schema_digest != target.schema_digest.as_str()
    {
        return Err("observation target does not match the checked target and manifest");
    }

    let bindings_by_coordinate = manifest
        .bindings
        .iter()
        .filter_map(|(capability_id, binding)| {
            if binding
                .required_evidence
                .contains(&EvidenceDomain::ExactTarget)
            {
                binding
                    .wire_coordinate
                    .as_ref()
                    .map(|coordinate| (coordinate.as_str(), capability_id.clone()))
            } else {
                None
            }
        })
        .collect::<BTreeMap<_, _>>();

    let mut categories = BTreeSet::new();
    let mut scoped = Vec::with_capacity(observations.observations.len());
    for observation in observations.observations {
        if !categories.insert(observation.category) {
            return Err("observation category is duplicated");
        }
        let field_coordinate = observation
            .operation
            .split_once('(')
            .map_or(observation.operation.as_str(), |(field, _)| field);
        let capability_id = bindings_by_coordinate
            .get(observation.operation.as_str())
            .or_else(|| bindings_by_coordinate.get(field_coordinate))
            .ok_or("observation operation has no exact-target binding")?
            .clone();
        scoped.push(ConformanceObservation {
            category: observation.category,
            operation: observation.operation,
            outcome: CheckOutcome::Passed,
            capability_ids: CanonicalSet::new([capability_id]),
        });
    }
    if categories != required_conformance_categories() {
        return Err("observation categories do not exhaust the required matrix");
    }

    let command = CommandSpec {
        program: ExecutableId::new("dagger").map_err(|_| "command program is invalid")?,
        args: vec!["call".to_owned(), "core-conformance".to_owned()],
        working_directory: RepositoryRelativePath::new("toolchains/rust-sdk-dev")
            .map_err(|_| "command working directory is invalid")?,
        environment: BTreeMap::new(),
    };
    let command_digest =
        canonical_digest(DigestDomain::Artifact, &command).map_err(|_| "command digest failed")?;
    let result_digest = canonical_digest(DigestDomain::Artifact, &scoped)
        .map_err(|_| "observation digest failed")?;
    let run = CoreConformanceRun {
        target_revision: target.dagger_revision.clone(),
        schema_digest: target.schema_digest.clone(),
        subject_revision,
        command,
        observations: scoped,
        result_digest,
    };
    let evidence = core_conformance_evidence(
        EvidenceId::new("verification/core-codegen/exact-target")
            .map_err(|_| "evidence ID is invalid")?,
        &run,
        &manifest,
        &command_digest,
    )
    .map_err(|_| "conformance evidence admission failed")?;
    fs::write(
        output,
        canonical_bytes(&evidence).map_err(|_| "evidence encoding failed")?,
    )
    .map_err(|_| "evidence output could not be written")?;
    Ok(())
}

fn read_canonical<T>(path: &Path) -> Result<T, &'static str>
where
    T: serde::de::DeserializeOwned + serde::Serialize,
{
    decode_canonical(&fs::read(path).map_err(|_| "contract input is unavailable")?)
        .map_err(|_| "contract input is not canonical JSON")
}
