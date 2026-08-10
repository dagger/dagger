//! Closed command-line boundary for private operation execution.
//!
//! Production exposes fixed operation and build-time packaging shapes. Inputs are
//! bounded typed values or strict canonical documents; there is no generic executable,
//! argument, environment, renderer, or descriptor override hidden behind another flag.

use std::ffi::OsString;
use std::fs;
use std::path::{Path, PathBuf};

use clap::{Arg, Command};

use crate::DigestDomain;
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::post_work::Cancellation;
use crate::runner::execute_operation;
use crate::{
    CanonicalRegistry, CanonicalRepositoryUrl, EngineSourceDescriptor, ExactRustToolchain,
    ExactVersion, FullRevision, OperationRequest, OperationRoot, PackageIdentity,
    PublishedSdkDependency, SdkPackageName, Sha256Digest, build_packaged_content, canonical_digest,
    decode_canonical,
};

const MAX_CONTROL_BYTES: u64 = 16 * 1024 * 1024;

/// Runs the closed CLI from an explicit argument iterator.
pub async fn run_from<I, T>(arguments: I) -> Result<(), EngineDiagnostic>
where
    I: IntoIterator<Item = T>,
    T: Into<OsString> + Clone,
{
    let matches = command().try_get_matches_from(arguments).map_err(|_| {
        EngineDiagnostic::new(
            EngineDiagnosticCode::OperationInputInvalid,
            Some("cli"),
            "command line does not match the fixed private operation interface",
        )
    })?;
    if let Some(package) = matches.subcommand_matches("package-content") {
        return package_content(package);
    }
    let Some(execute) = matches.subcommand_matches("execute") else {
        return Ok(());
    };
    let request_path = required_path(execute, "request")?;
    let schema_path = required_path(execute, "schema")?;
    let descriptor_path = required_path(execute, "descriptor")?;
    let project_path = required_path(execute, "project")?;
    let request = read_control(&request_path, "request")?;
    let schema = read_control(&schema_path, "schema")?;
    let descriptor = read_control(&descriptor_path, "descriptor")?;
    let request: OperationRequest = decode_canonical(&request)
        .map_err(|_| invalid("request", "operation request is not strict canonical JSON"))?;
    let descriptor: EngineSourceDescriptor = decode_canonical(&descriptor).map_err(|_| {
        invalid(
            "descriptor",
            "engine source descriptor is not strict canonical JSON",
        )
    })?;
    let root = OperationRoot::open(project_path)?;
    execute_operation(
        &root,
        &request,
        &schema,
        &descriptor,
        &Cancellation::default(),
    )
    .await?;
    Ok(())
}

/// Returns the complete production command shape for help and parser tests.
#[must_use]
pub fn command() -> Command {
    Command::new("dagger-rust-engine")
        .version(env!("CARGO_PKG_VERSION"))
        .disable_help_subcommand(true)
        .subcommand(
            Command::new("execute")
                .about("Execute one typed Rust SDK generation operation")
                .arg(required("request"))
                .arg(required("schema"))
                .arg(required("descriptor"))
                .arg(required("project")),
        )
        .subcommand(
            Command::new("package-content")
                .about("Seal one Rust SDK content root with canonical provenance")
                .arg(required("root"))
                .arg(required("repository"))
                .arg(required("dagger-revision"))
                .arg(required("engine-version"))
                .arg(required("rust-sdk-version"))
                .arg(required("rust-toolchain"))
                .arg(required("core-schema-digest"))
                .arg(required("dependency-kind"))
                .arg(required("dependency-value")),
        )
}

fn package_content(matches: &clap::ArgMatches) -> Result<(), EngineDiagnostic> {
    let root = required_path(matches, "root")?;
    let repository = scalar::<CanonicalRepositoryUrl>(matches, "repository")?;
    let dagger_revision = scalar::<FullRevision>(matches, "dagger-revision")?;
    let engine_version = scalar::<ExactVersion>(matches, "engine-version")?;
    let rust_sdk_version = scalar::<ExactVersion>(matches, "rust-sdk-version")?;
    let rust_toolchain = scalar::<ExactRustToolchain>(matches, "rust-toolchain")?;
    let core_schema_digest = scalar::<Sha256Digest>(matches, "core-schema-digest")?;
    let dependency_value = required_value(matches, "dependency-value")?;
    let sdk_dependency = match required_value(matches, "dependency-kind")?.as_str() {
        "registry" => PublishedSdkDependency::Registry {
            registry: CanonicalRegistry::new("crates-io".to_owned())
                .expect("the canonical registry constant is valid"),
            package: SdkPackageName::new("dagger-sdk".to_owned())
                .expect("the public SDK package constant is valid"),
            exact_version: dependency_value.parse().map_err(|_| {
                invalid(
                    "dependency-value",
                    "registry dependency must be an exact semantic version",
                )
            })?,
        },
        "git" => PublishedSdkDependency::Git {
            url: repository.clone(),
            revision: dependency_value.parse().map_err(|_| {
                invalid(
                    "dependency-value",
                    "Git dependency must be a full immutable revision",
                )
            })?,
            package: SdkPackageName::new("dagger-sdk".to_owned())
                .expect("the public SDK package constant is valid"),
        },
        _ => {
            return Err(invalid(
                "dependency-kind",
                "dependency kind must be registry or git",
            ));
        }
    };
    let (_, descriptor) = build_packaged_content(
        &root,
        PackageIdentity {
            repository,
            dagger_revision,
            engine_version,
            rust_sdk_version,
            rust_toolchain,
            sdk_dependency,
            core_schema_digest,
        },
    )?;
    let digest = canonical_digest(DigestDomain::EngineSource, &descriptor).map_err(|_| {
        invalid(
            "descriptor",
            "engine source descriptor identity could not be computed",
        )
    })?;
    println!("{digest}");
    Ok(())
}

/// Serializes one bounded structured diagnostic for private stderr.
#[must_use]
pub fn render_diagnostic(diagnostic: &EngineDiagnostic) -> String {
    serde_json::to_string(diagnostic).unwrap_or_else(|_| {
        "{\"code\":\"GENERATION_FAILED\",\"message\":\"diagnostic encoding failed\"}".to_owned()
    })
}

fn required(name: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .value_name("PATH")
        .required(true)
        .num_args(1)
}

fn required_path(
    matches: &clap::ArgMatches,
    name: &'static str,
) -> Result<PathBuf, EngineDiagnostic> {
    matches
        .get_one::<String>(name)
        .map(PathBuf::from)
        .ok_or_else(|| invalid("cli", "required fixed path argument is missing"))
}

fn required_value(
    matches: &clap::ArgMatches,
    name: &'static str,
) -> Result<String, EngineDiagnostic> {
    matches
        .get_one::<String>(name)
        .cloned()
        .ok_or_else(|| invalid("cli", "required fixed value argument is missing"))
}

fn scalar<T>(matches: &clap::ArgMatches, name: &'static str) -> Result<T, EngineDiagnostic>
where
    T: std::str::FromStr,
{
    required_value(matches, name)?.parse().map_err(|_| {
        invalid(
            name,
            "value does not satisfy the packaged identity contract",
        )
    })
}

fn read_control(path: &Path, coordinate: &str) -> Result<Vec<u8>, EngineDiagnostic> {
    let metadata = fs::symlink_metadata(path)
        .map_err(|_| invalid(coordinate, "required operation input is missing"))?;
    if metadata.file_type().is_symlink() || !metadata.file_type().is_file() {
        return Err(invalid(
            coordinate,
            "operation input must be a regular non-symlink file",
        ));
    }
    if metadata.len() > MAX_CONTROL_BYTES {
        return Err(invalid(
            coordinate,
            "operation input exceeds its byte bound",
        ));
    }
    fs::read(path).map_err(|_| invalid(coordinate, "operation input could not be read"))
}

fn invalid(coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::OperationInputInvalid,
        Some(coordinate),
        message,
    )
}
