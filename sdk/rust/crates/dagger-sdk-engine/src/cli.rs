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
use crate::client::initialization::execute_client_initialization;
use crate::client::workspace::plan_client_set;
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::initialization::execute_initialization;
use crate::post_work::Cancellation;
use crate::runner::execute_operation;
use crate::runtime::{finalize_runtime, verify_runtime};
use crate::{
    CanonicalRegistry, CanonicalRepositoryUrl, EngineExecutionRequest, EngineSourceDescriptor,
    ExactRustToolchain, ExactVersion, ExecutionResult, ExecutionResultKind, FullRevision,
    OperationRoot, PackageIdentity, PublishedSdkDependency, RuntimeBuildPlan, RuntimePolicy,
    RuntimeVerificationRequest, SdkPackageName, Sha256Digest, build_packaged_content,
    canonical_bytes, canonical_digest, decode_canonical,
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
    match matches.subcommand() {
        Some(("package-content", package)) => package_content(package),
        Some(("execute", execute_matches)) => execute(execute_matches).await,
        Some(("verify-runtime", verify)) => verify_runtime_command(verify).await,
        Some(("finalize-runtime", finalize)) => finalize_runtime_command(finalize),
        _ => Ok(()),
    }
}

async fn execute(matches: &clap::ArgMatches) -> Result<(), EngineDiagnostic> {
    let request_path = required_path(matches, "request")?;
    let descriptor_path = required_path(matches, "descriptor")?;
    let project_path = required_path(matches, "project")?;
    let result_path = required_path(matches, "result")?;
    let request = read_control(&request_path, "request")?;
    let descriptor = read_control(&descriptor_path, "descriptor")?;
    let request: EngineExecutionRequest = decode_canonical(&request)
        .map_err(|_| invalid("request", "operation request is not strict canonical JSON"))?;
    let descriptor: EngineSourceDescriptor = decode_canonical(&descriptor).map_err(|_| {
        invalid(
            "descriptor",
            "engine source descriptor is not strict canonical JSON",
        )
    })?;
    let root = OperationRoot::open(project_path)?;
    let result = match request {
        EngineExecutionRequest::InitializeModule(request) => {
            if matches.get_one::<String>("schema").is_some() {
                return Err(invalid(
                    "schema",
                    "initialization must not receive a visible schema",
                ));
            }
            execute_initialization(&root, &request, &descriptor, &Cancellation::default()).await?
        }
        EngineExecutionRequest::InitializeClient(request) => {
            if matches.get_one::<String>("schema").is_some() {
                return Err(invalid(
                    "schema",
                    "client initialization must not receive a visible schema",
                ));
            }
            execute_client_initialization(&root, &request, &descriptor)?
        }
        EngineExecutionRequest::PlanClientSet(request) => {
            if matches.get_one::<String>("schema").is_some() {
                return Err(invalid(
                    "schema",
                    "client-set planning must not receive a visible schema",
                ));
            }
            let output_root = request.cwd.clone();
            let client_plan = plan_client_set(request)?;
            ExecutionResult {
                format_version: crate::FormatVersion,
                kind: ExecutionResultKind::ClientPlan,
                output_root,
                touched_paths: Default::default(),
                operation_manifest: None,
                vcs_generated: Default::default(),
                vcs_ignored: Default::default(),
                client_plan: Some(client_plan),
            }
        }
        EngineExecutionRequest::Generate(request) => {
            let schema_path = optional_path(matches, "schema").ok_or_else(|| {
                invalid(
                    "schema",
                    "generation requires the fixed visible-schema input",
                )
            })?;
            let schema = read_control(&schema_path, "schema")?;
            execute_operation(
                &root,
                &request,
                &schema,
                &descriptor,
                &Cancellation::default(),
            )
            .await?
        }
    };
    write_control(
        &result_path,
        "result",
        &canonical_bytes(&result).map_err(|_| {
            invalid(
                "result",
                "operation result could not be canonically encoded",
            )
        })?,
    )
}

async fn verify_runtime_command(matches: &clap::ArgMatches) -> Result<(), EngineDiagnostic> {
    let request: RuntimeVerificationRequest = decode_canonical(&read_control(
        &required_path(matches, "request")?,
        "request",
    )?)
    .map_err(|_| invalid("request", "runtime request is not strict canonical JSON"))?;
    let descriptor: EngineSourceDescriptor = decode_canonical(&read_control(
        &required_path(matches, "descriptor")?,
        "descriptor",
    )?)
    .map_err(|_| {
        invalid(
            "descriptor",
            "engine descriptor is not strict canonical JSON",
        )
    })?;
    let policy: RuntimePolicy =
        decode_canonical(&read_control(&required_path(matches, "policy")?, "policy")?)
            .map_err(|_| invalid("policy", "runtime policy is not strict canonical JSON"))?;
    let root = OperationRoot::open(required_path(matches, "project")?)?;
    let plan = verify_runtime(
        &root,
        &request,
        &descriptor,
        &policy,
        &Cancellation::default(),
    )
    .await?;
    write_control(
        &required_path(matches, "output")?,
        "output",
        &canonical_bytes(&plan)
            .map_err(|_| invalid("output", "runtime plan could not be canonically encoded"))?,
    )
}

fn finalize_runtime_command(matches: &clap::ArgMatches) -> Result<(), EngineDiagnostic> {
    let plan: RuntimeBuildPlan =
        decode_canonical(&read_control(&required_path(matches, "plan")?, "plan")?)
            .map_err(|_| invalid("plan", "runtime plan is not strict canonical JSON"))?;
    let policy: RuntimePolicy =
        decode_canonical(&read_control(&required_path(matches, "policy")?, "policy")?)
            .map_err(|_| invalid("policy", "runtime policy is not strict canonical JSON"))?;
    let provenance = finalize_runtime(&plan, &required_path(matches, "binary")?, &policy)?;
    write_control(
        &required_path(matches, "output")?,
        "output",
        &canonical_bytes(&provenance).map_err(|_| {
            invalid(
                "output",
                "runtime provenance could not be canonically encoded",
            )
        })?,
    )
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
                .arg(optional("schema"))
                .arg(required("descriptor"))
                .arg(required("project"))
                .arg(required("result")),
        )
        .subcommand(
            Command::new("verify-runtime")
                .about("Verify committed Rust module inputs and emit one fixed build plan")
                .arg(required("request"))
                .arg(required("descriptor"))
                .arg(required("policy"))
                .arg(required("project"))
                .arg(required("output")),
        )
        .subcommand(
            Command::new("finalize-runtime")
                .about("Hash the fixed runtime binary and finalize canonical provenance")
                .arg(required("plan"))
                .arg(required("policy"))
                .arg(required("binary"))
                .arg(required("output")),
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
                .arg(required("dependency-value"))
                .arg(optional("dependency-repository")),
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
            url: scalar::<CanonicalRepositoryUrl>(matches, "dependency-repository")?,
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
    canonical_bytes(diagnostic)
		.map(|bytes| {
			// The process entrypoint supplies the terminal newline. Removing the
			// encoder's newline here keeps the bytes canonical after `eprintln!`.
			let payload = bytes.strip_suffix(b"\n").unwrap_or(&bytes);
			String::from_utf8_lossy(payload).into_owned()
		})
		.unwrap_or_else(|_| {
			"{\n  \"causes\": [],\n  \"code\": \"GENERATION_FAILED\",\n  \"coordinate\": \"diagnostic\",\n  \"message\": \"diagnostic encoding failed\"\n}".to_owned()
		})
}

fn required(name: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .value_name("PATH")
        .required(true)
        .num_args(1)
}

fn optional(name: &'static str) -> Arg {
    Arg::new(name).long(name).value_name("PATH").num_args(1)
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

fn optional_path(matches: &clap::ArgMatches, name: &'static str) -> Option<PathBuf> {
    matches.get_one::<String>(name).map(PathBuf::from)
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

fn write_control(path: &Path, coordinate: &str, bytes: &[u8]) -> Result<(), EngineDiagnostic> {
    let parent = path
        .parent()
        .ok_or_else(|| invalid(coordinate, "control output has no parent directory"))?;
    let metadata = fs::symlink_metadata(parent)
        .map_err(|_| invalid(coordinate, "control output parent is missing"))?;
    if metadata.file_type().is_symlink() || !metadata.is_dir() {
        return Err(invalid(
            coordinate,
            "control output parent must be a real directory",
        ));
    }
    if fs::symlink_metadata(path).is_ok_and(|metadata| metadata.file_type().is_symlink()) {
        return Err(invalid(
            coordinate,
            "control output must not replace a symlink",
        ));
    }
    let temporary = path.with_extension("tmp");
    fs::write(&temporary, bytes)
        .map_err(|_| invalid(coordinate, "control output could not be staged"))?;
    fs::rename(temporary, path)
        .map_err(|_| invalid(coordinate, "control output could not be published"))
}

fn invalid(coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::OperationInputInvalid,
        Some(coordinate),
        message,
    )
}
