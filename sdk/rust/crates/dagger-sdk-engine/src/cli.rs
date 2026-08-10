//! Closed command-line boundary for private operation execution.
//!
//! Production exposes one fixed `execute` shape. Inputs are bounded regular files and
//! strict canonical documents; there is no generic executable, argument, environment,
//! renderer, or descriptor override hidden behind another flag.

use std::ffi::OsString;
use std::fs;
use std::path::{Path, PathBuf};

use clap::{Arg, Command};

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::post_work::Cancellation;
use crate::runner::execute_operation;
use crate::{EngineSourceDescriptor, OperationRequest, OperationRoot, decode_canonical};

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
