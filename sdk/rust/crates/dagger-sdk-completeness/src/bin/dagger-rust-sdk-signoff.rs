//! Private typed host-preflight and SDK-sign-off command boundary.
//!
//! The initial command accepts one checked profile and one output path. All executed host actions
//! are selected by the closed Rust plan; callers cannot supply provider arguments or commands.

use std::collections::BTreeMap;
use std::ffi::OsStr;
use std::fs;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Command, ExitCode, Stdio};
use std::str::FromStr;
use std::thread;
use std::time::{Duration, Instant};

use clap::{Arg, Command as ClapCommand, value_parser};
use dagger_sdk_completeness::{
    ArtifactBuildObservation, ArtifactComponent, ArtifactImportObservation,
    ArtifactMaterialization, ArtifactPlan, ArtifactPlanSeed, ArtifactSecurityObservation,
    ArtifactSecurityReport, AssertionCatalogInput, CanonicalSet, CaseCatalog, CaseCatalogInput,
    CaseProgram, ChildClosure, ChildClosureReference, ChildEvidenceFormat, ClosureOutcome,
    ClosureSubjectBinding, CommitSha, ConcurrencyClass, ConformanceDiagnostic,
    ConformanceDiagnosticCode, ConformanceDiagnosticSet, ConformanceFormatVersion,
    ConformanceScopeInput, ContainerDaemonObservation, DiagnosticCoordinate, DiagnosticPhase,
    Digest, DigestDomain, ExceptionEvaluationContext, ExternalInputRole,
    ExternalProvenanceRegistry, FixtureRegistryInput, GeneratedAssetDomain, HostPreflightPlan,
    HostPreflightRecord, HostPreflightStep, HostProbe, HostProbeError, HostProbeErrorKind,
    HostResourceObservation, HostStepObservation, HostStepResult, ImplementationClosureBundle,
    ImplementationClosureBundleInput, NetworkPolicyId, NonEmptyText, NonZeroBytes, NonZeroCount,
    NonZeroMillis, PackagedArtifactKind, PackagedArtifactScanBundle, PlatformDescriptor,
    RawSignoffFacadeObservation, RepositoryRelativePath, ResolvedLedger, RetryPolicy,
    ReviewedConformanceScope, RustDependencySecurityReport, RustFirstConformanceManifestInput,
    RustScenarioRegistryInput, RustSdkDependencyDescriptor, SecretEvidenceInput,
    SecretInspectionDomain, SensitiveIdentitySet, SignoffAdmissionContext, SignoffCaseId,
    SignoffHostProfile, SignoffNetworkPolicy, SignoffRunPlan, SubjectIdentity,
    SupportedNativePlatformSet, TargetDescriptor, TargetDigest, ToolchainRole, UtcDate,
    VerdictDecision, admit_artifact_build_receipt, admit_artifact_import_receipt,
    admit_artifact_security, admit_rust_dependency_security, admit_secret_evidence,
    admit_signoff_facade_observation, artifact_build_receipt, artifact_import_plan,
    artifact_import_receipt, artifact_manifest_for_payload, artifact_provenance_document,
    assemble_artifact_bundle, assemble_implementation_closure_bundle,
    assemble_packaged_artifact_scan_bundle, assemble_signoff_run_plan,
    assemble_supported_native_platform_set, canonical_bytes, canonical_digest,
    compile_assertion_catalog, compile_case_catalog, compile_facade_route_registry,
    compile_fixture_registry, compile_observable_fixture_program_registry,
    compile_rust_scenario_registry, decode_artifact_bundle, decode_atomic_signoff_verdict,
    decode_canonical, derive_atomic_signoff_verdict, derive_conformance_scope,
    derive_release_handoff, encode_atomic_signoff_verdict, encode_release_handoff,
    expected_closure_plan, inspect_canary_chunks, plan_host_preflight,
    render_atomic_signoff_verdict, render_implementation_closure_bundle, render_release_handoff,
    reviewed_rust_dependency_security_observation, run_host_preflight, rust_artifact_digest,
    sanitize_durable_evidence, scan_packaged_artifact, scan_retained_output,
    seal_artifact_build_plan, secret_canary_set_from_entropy, secret_evidence_domain_byte_limit,
    signoff_run_plan_digest, translate_trivy_artifact_scan, validate_artifact_security_report,
    validate_host_preflight_record, validate_secret_evidence_report, verify_artifact_import_source,
};
use serde::Serialize;

const MAX_PROCESS_OUTPUT: usize = 1024 * 1024;
const MAX_FACADE_ADMISSION_OUTPUT: usize = 8 * 1024 * 1024;
const MAX_CANARY_SEED_TEXT: u64 = 66;
const CANARY: &[u8] = b"dagger-preflight-canary-80986dbe61454709a738130c13f3d0db";
const SMOKE_CONTAINER: &str = "dagger-rust-sdk-preflight-smoke";
const CACHE_IMAGE: &str = "dagger-rust-sdk-preflight-cache:current";
const SMOKE_IMAGE: &str = "registry.dagger.io/engine@sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e";

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(BinaryError::Contract(errors)) => {
            for error in errors.as_slice() {
                eprintln!("{} {:?}", error.code, error.coordinate.phase);
            }
            ExitCode::from(1)
        }
        Err(BinaryError::Operational(message)) => {
            eprintln!("{message}");
            ExitCode::from(2)
        }
    }
}

enum BinaryError {
    Contract(dagger_sdk_completeness::ConformanceDiagnosticSet),
    Operational(&'static str),
}

impl From<dagger_sdk_completeness::ConformanceDiagnosticSet> for BinaryError {
    fn from(value: dagger_sdk_completeness::ConformanceDiagnosticSet) -> Self {
        Self::Contract(value)
    }
}

fn run() -> Result<(), BinaryError> {
    let matches = ClapCommand::new("dagger-rust-sdk-signoff")
        .about("Private Dagger Rust SDK sign-off tooling")
        .subcommand_required(true)
        .subcommand(
            ClapCommand::new("preflight")
                .about("Validate one checked provider-neutral sign-off host profile")
                .arg(
                    Arg::new("profile")
                        .long("profile")
                        .required(true)
                        .value_parser(value_parser!(PathBuf)),
                )
                .arg(
                    Arg::new("output")
                        .long("output")
                        .required(true)
                        .value_parser(value_parser!(PathBuf)),
                ),
        )
        .subcommand(
            ClapCommand::new("artifact-seed")
                .about("Derive one clean reachable focused-artifact seed from the checked tree")
                .arg(path_argument("root"))
                .arg(Arg::new("repository").long("repository").required(true))
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("artifact-plan")
                .about("Seal one Build plan from independently observed component identities")
                .arg(path_argument("seed"))
                .arg(
                    Arg::new("engine-content-digest")
                        .long("engine-content-digest")
                        .required(true),
                )
                .arg(
                    Arg::new("cli-content-digest")
                        .long("cli-content-digest")
                        .required(true),
                )
                .arg(
                    Arg::new("go-runtime-content-digest")
                        .long("go-runtime-content-digest")
                        .required(true),
                )
                .arg(
                    Arg::new("rust-content-digest")
                        .long("rust-content-digest")
                        .required(true),
                )
                .arg(
                    Arg::new("rust-descriptor-digest")
                        .long("rust-descriptor-digest")
                        .required(true),
                )
                .arg(path_argument("rust-dependency-descriptor"))
                .arg(
                    Arg::new("rust-dependency-descriptor-digest")
                        .long("rust-dependency-descriptor-digest")
                        .required(true),
                )
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("artifact-build")
                .about("Assemble one canonical exact-target bundle from existing OCI bytes")
                .arg(path_argument("plan"))
                .arg(path_argument("payload"))
                .arg(path_argument("observation"))
                .arg(path_argument("bundle-output"))
                .arg(path_argument("manifest-output"))
                .arg(path_argument("receipt-output")),
        )
        .subcommand(
            ClapCommand::new("artifact-extract")
                .about("Verify one Import source and extract its retained OCI bytes")
                .arg(path_argument("plan"))
                .arg(path_argument("bundle"))
                .arg(path_argument("payload-output"))
                .arg(path_argument("manifest-output")),
        )
        .subcommand(
            ClapCommand::new("artifact-import")
                .about("Admit actual Import graph evidence and emit its canonical receipt")
                .arg(path_argument("plan"))
                .arg(path_argument("bundle"))
                .arg(path_argument("observation"))
                .arg(path_argument("receipt-output")),
        )
        .subcommand(
            ClapCommand::new("scanner-translate")
                .about("Translate bounded Trivy files into one canonical Rust observation")
                .arg(path_argument("root"))
                .arg(path_argument("findings"))
                .arg(path_argument("scanner-version"))
                .arg(path_argument("database-metadata"))
                .arg(path_argument("database-checksums"))
                .arg(
                    Arg::new("database-artifact-digest")
                        .long("database-artifact-digest")
                        .required(true),
                )
                .arg(path_argument("payload-checksum"))
                .arg(
                    Arg::new("elapsed-millis")
                        .long("elapsed-millis")
                        .required(true)
                        .value_parser(value_parser!(u64)),
                )
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("security-report")
                .about("Admit the exact scanner observation into one Rust security report")
                .arg(path_argument("root"))
                .arg(path_argument("plan"))
                .arg(path_argument("bundle"))
                .arg(path_argument("rust-security"))
                .arg(path_argument("observation"))
                .arg(Arg::new("policy-date").long("policy-date").required(true))
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("rust-security-report")
                .about("Emit the ordinary Rust security closure after external gates pass")
                .arg(path_argument("root"))
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("packaged-scan")
                .about("Inspect the fixed actual build-only example outputs")
                .arg(path_argument("root"))
                .arg(path_argument("seed"))
                .arg(Arg::new("cli-digest").long("cli-digest").required(true))
                .arg(
                    Arg::new("backend-digest")
                        .long("backend-digest")
                        .required(true),
                )
                .arg(
                    Arg::new("frontend-digest")
                        .long("frontend-digest")
                        .required(true),
                )
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("secret-report")
                .about("Scan the fixed exact-sign-off evidence domains with ephemeral canaries")
                .arg(path_argument("root"))
                .arg(path_argument("seed"))
                .arg(path_argument("packaged-scan"))
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("implementation-closure")
                .about("Assemble the exact current six-domain engine-free closure")
                .arg(path_argument("root"))
                .arg(path_argument("platform"))
                .arg(path_argument("rust-security"))
                .arg(path_argument("output"))
                .arg(path_argument("markdown-output")),
        )
        .subcommand(
            ClapCommand::new("run-plan")
                .about("Assemble the fixed authoritative Import sign-off plan")
                .arg(path_argument("root"))
                .arg(path_argument("build-plan"))
                .arg(path_argument("bundle"))
                .arg(path_argument("closure"))
                .arg(path_argument("platform"))
                .arg(path_argument("host-profile"))
                .arg(path_argument("preflight"))
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("facade-admit")
                .about("Admit all engine-free inputs before constructing the Dagger target graph")
                .arg(path_argument("root"))
                .arg(path_argument("plan"))
                .arg(path_argument("bundle"))
                .arg(path_argument("catalog"))
                .arg(path_argument("closure"))
                .arg(path_argument("platform"))
                .arg(path_argument("host-profile"))
                .arg(path_argument("preflight"))
                .arg(path_argument("output")),
        )
        .subcommand(
            ClapCommand::new("verdict")
                .about("Translate one complete raw graph observation into the atomic Rust verdict")
                .arg(path_argument("root"))
                .arg(path_argument("plan"))
                .arg(path_argument("closure"))
                .arg(path_argument("platform"))
                .arg(path_argument("security"))
                .arg(path_argument("bundle"))
                .arg(path_argument("observation"))
                .arg(path_argument("output"))
                .arg(path_argument("markdown-output")),
        )
        .subcommand(
            ClapCommand::new("handoff")
                .about("Derive the evidence-only Feature 9 handoff from retained passing bytes")
                .arg(path_argument("bundle"))
                .arg(path_argument("verdict"))
                .arg(path_argument("output"))
                .arg(path_argument("markdown-output")),
        )
        .get_matches();
    match matches.subcommand().expect("subcommand is required") {
        ("preflight", values) => preflight(
            required_path(values, "profile"),
            required_path(values, "output"),
        ),
        ("artifact-seed", values) => artifact_seed(
            required_path(values, "root"),
            values.get_one::<String>("repository").unwrap(),
            required_path(values, "output"),
        ),
        ("artifact-plan", values) => artifact_plan(
            required_path(values, "seed"),
            values.get_one::<String>("engine-content-digest").unwrap(),
            values.get_one::<String>("cli-content-digest").unwrap(),
            values
                .get_one::<String>("go-runtime-content-digest")
                .unwrap(),
            values.get_one::<String>("rust-content-digest").unwrap(),
            values.get_one::<String>("rust-descriptor-digest").unwrap(),
            required_path(values, "rust-dependency-descriptor"),
            values
                .get_one::<String>("rust-dependency-descriptor-digest")
                .unwrap(),
            required_path(values, "output"),
        ),
        ("artifact-build", values) => artifact_build(
            required_path(values, "plan"),
            required_path(values, "payload"),
            required_path(values, "observation"),
            required_path(values, "bundle-output"),
            required_path(values, "manifest-output"),
            required_path(values, "receipt-output"),
        ),
        ("artifact-extract", values) => artifact_extract(
            required_path(values, "plan"),
            required_path(values, "bundle"),
            required_path(values, "payload-output"),
            required_path(values, "manifest-output"),
        ),
        ("artifact-import", values) => artifact_import(
            required_path(values, "plan"),
            required_path(values, "bundle"),
            required_path(values, "observation"),
            required_path(values, "receipt-output"),
        ),
        ("scanner-translate", values) => scanner_translate(
            required_path(values, "root"),
            required_path(values, "findings"),
            required_path(values, "scanner-version"),
            required_path(values, "database-metadata"),
            required_path(values, "database-checksums"),
            values
                .get_one::<String>("database-artifact-digest")
                .expect("clap requires database artifact digest"),
            required_path(values, "payload-checksum"),
            *values
                .get_one::<u64>("elapsed-millis")
                .expect("clap requires elapsed millis"),
            required_path(values, "output"),
        ),
        ("security-report", values) => security_report(
            required_path(values, "root"),
            required_path(values, "plan"),
            required_path(values, "bundle"),
            required_path(values, "rust-security"),
            required_path(values, "observation"),
            values
                .get_one::<String>("policy-date")
                .expect("clap requires policy date"),
            required_path(values, "output"),
        ),
        ("rust-security-report", values) => rust_security_report(
            required_path(values, "root"),
            required_path(values, "output"),
        ),
        ("packaged-scan", values) => packaged_scan(
            required_path(values, "root"),
            required_path(values, "seed"),
            values
                .get_one::<String>("cli-digest")
                .expect("clap requires CLI digest"),
            values
                .get_one::<String>("backend-digest")
                .expect("clap requires backend digest"),
            values
                .get_one::<String>("frontend-digest")
                .expect("clap requires frontend digest"),
            required_path(values, "output"),
        ),
        ("secret-report", values) => secret_report(
            required_path(values, "root"),
            required_path(values, "seed"),
            required_path(values, "packaged-scan"),
            required_path(values, "output"),
        ),
        ("implementation-closure", values) => implementation_closure(
            required_path(values, "root"),
            required_path(values, "platform"),
            required_path(values, "rust-security"),
            required_path(values, "output"),
            required_path(values, "markdown-output"),
        ),
        ("run-plan", values) => run_plan(
            required_path(values, "root"),
            required_path(values, "build-plan"),
            required_path(values, "bundle"),
            required_path(values, "closure"),
            required_path(values, "platform"),
            required_path(values, "host-profile"),
            required_path(values, "preflight"),
            required_path(values, "output"),
        ),
        ("facade-admit", values) => facade_admit(
            required_path(values, "root"),
            required_path(values, "plan"),
            required_path(values, "bundle"),
            required_path(values, "catalog"),
            required_path(values, "closure"),
            required_path(values, "platform"),
            required_path(values, "host-profile"),
            required_path(values, "preflight"),
            required_path(values, "output"),
        ),
        ("verdict", values) => verdict(
            required_path(values, "root"),
            required_path(values, "plan"),
            required_path(values, "closure"),
            required_path(values, "platform"),
            required_path(values, "security"),
            required_path(values, "bundle"),
            required_path(values, "observation"),
            required_path(values, "output"),
            required_path(values, "markdown-output"),
        ),
        ("handoff", values) => handoff(
            required_path(values, "bundle"),
            required_path(values, "verdict"),
            required_path(values, "output"),
            required_path(values, "markdown-output"),
        ),
        _ => unreachable!("clap admits only the closed sign-off commands"),
    }
}

#[allow(clippy::too_many_arguments)]
fn run_plan(
    root: &Path,
    build_plan_path: &Path,
    bundle_path: &Path,
    closure_path: &Path,
    platform_path: &Path,
    host_profile_path: &Path,
    preflight_path: &Path,
    output_path: &Path,
) -> Result<(), BinaryError> {
    let build_plan: ArtifactPlan = read_canonical(
        build_plan_path,
        "could not read canonical Build artifact plan",
    )?;
    let bundle = decode_artifact_bundle(
        &fs::read(bundle_path)
            .map_err(|_| BinaryError::Operational("could not read retained artifact bundle"))?,
    )?;
    let artifact_plan = artifact_import_plan(&build_plan, &bundle)?;
    let closure: ImplementationClosureBundle =
        read_canonical(closure_path, "could not read implementation closure")?;
    let platform: SupportedNativePlatformSet = read_canonical(
        platform_path,
        "could not read supported native-platform set",
    )?;
    let profile: SignoffHostProfile =
        read_canonical(host_profile_path, "could not read sign-off host profile")?;
    let preflight: HostPreflightRecord =
        read_canonical(preflight_path, "could not read sign-off host preflight")?;
    let (plan, _) =
        assemble_checked_run_plan(root, artifact_plan, closure, platform, profile, preflight)?;
    let bytes = canonical_bytes(&plan)
        .map_err(|_| BinaryError::Operational("could not encode authoritative sign-off plan"))?;
    write_new(output_path, &bytes)
}

fn assemble_checked_run_plan(
    root: &Path,
    artifact_plan: ArtifactPlan,
    closure: ImplementationClosureBundle,
    platform: SupportedNativePlatformSet,
    profile: SignoffHostProfile,
    preflight: HostPreflightRecord,
) -> Result<(SignoffRunPlan, CheckedSignoffPolicy), BinaryError> {
    let rebuilt_closure =
        assemble_implementation_closure_bundle(ImplementationClosureBundleInput {
            format_version: closure.format_version,
            target_digest: closure.target_digest.clone(),
            subject: closure.subject.clone(),
            child_closures: closure.child_closures.values().cloned().collect(),
            compatible_assets: closure.compatible_assets.values().cloned().collect(),
            generated_assets: closure.generated_assets.clone(),
            platform_matrix_digest: closure.platform_matrix_digest.clone(),
            rust_security_digest: closure.rust_security_digest.clone(),
            plan: closure.plan.iter().cloned().collect(),
        })?;
    let rebuilt_platform = assemble_supported_native_platform_set(
        platform.target_digest.clone(),
        platform.native_observations.values().cloned().collect(),
    )?;
    let host_plan = plan_host_preflight(profile)?;
    validate_host_preflight_record(
        &host_plan,
        &preflight,
        &preflight.container_daemon.daemon_identity,
    )?;
    let policy = checked_signoff_policy(root)?;
    let source_matches = match &closure.subject {
        SubjectIdentity::Revision(revision) => revision == &artifact_plan.subject.revision,
        SubjectIdentity::SourceDigest(digest) => {
            digest == &artifact_plan.subject.focused_source_digest
        }
    };
    if rebuilt_closure != closure
        || rebuilt_platform != platform
        || !source_matches
        || closure.target_digest != artifact_plan.target_descriptor_digest
        || platform.target_digest != artifact_plan.target_descriptor_digest
        || closure.platform_matrix_digest != platform.observation_set_digest
    {
        return Err(BinaryError::Operational(
            "run-plan closure platform artifact or subject identity is stale",
        ));
    }
    let plan = assemble_signoff_run_plan(
        artifact_plan,
        closure.bundle_digest,
        &policy.catalog,
        &policy.routes,
        host_plan.profile_digest,
        preflight.record_digest,
    )?;
    Ok((plan, policy))
}

#[derive(Serialize)]
struct FacadeAdmissionRoute {
    case_id: SignoffCaseId,
    program: CaseProgram,
    fixture_digest: Digest,
    boundary: String,
    execution_selector: String,
    executed: bool,
    timeout: NonZeroMillis,
    retry: RetryPolicy,
    network: NetworkPolicyId,
    concurrency_class: ConcurrencyClass,
}

#[derive(Serialize)]
struct FacadeAdmissionBody {
    format_version: ConformanceFormatVersion,
    run_plan_digest: Digest,
    target_digest: TargetDigest,
    subject_revision: CommitSha,
    platform: PlatformDescriptor,
    host_profile_digest: Digest,
    preflight_digest: Digest,
    artifact_plan: ArtifactPlan,
    artifact_bundle_digest: Digest,
    artifact_manifest_digest: Digest,
    artifact_payload_digest: Digest,
    closure_bundle_digest: Digest,
    platform_matrix_digest: Digest,
    case_catalog_digest: Digest,
    route_registry_digest: Digest,
    network_policies: BTreeMap<NetworkPolicyId, SignoffNetworkPolicy>,
    maximum_concurrency: NonZeroCount,
    expected_case_executions: NonZeroCount,
    total_budget: NonZeroMillis,
    routes: Vec<FacadeAdmissionRoute>,
}

#[derive(Serialize)]
struct FacadeAdmissionProjection {
    projection_digest: Digest,
    #[serde(flatten)]
    body: FacadeAdmissionBody,
}

#[allow(clippy::too_many_arguments)]
fn facade_admit(
    root: &Path,
    plan_path: &Path,
    bundle_path: &Path,
    catalog_path: &Path,
    closure_path: &Path,
    platform_path: &Path,
    host_profile_path: &Path,
    preflight_path: &Path,
    output_path: &Path,
) -> Result<(), BinaryError> {
    let supplied_plan: SignoffRunPlan =
        read_canonical(plan_path, "could not read canonical sign-off plan")?;
    let supplied_catalog: CaseCatalogInput =
        read_canonical(catalog_path, "could not read supplied conformance cases")?;
    let checked_catalog: CaseCatalogInput = read_canonical(
        &root.join("sdk/rust/completeness/conformance-cases.json"),
        "could not read checked conformance cases",
    )?;
    if supplied_catalog != checked_catalog {
        return Err(BinaryError::Operational(
            "supplied conformance cases differ from the checked Rust policy input",
        ));
    }
    let bundle = decode_artifact_bundle(
        &fs::read(bundle_path)
            .map_err(|_| BinaryError::Operational("could not read retained artifact bundle"))?,
    )?;
    // Import is re-derived from the verified bytes rather than trusting the caller's embedded
    // manifest or payload identity. The later exact-plan equality check rejects either mutation.
    let mut build_plan = supplied_plan.artifact_plan.clone();
    build_plan.materialization = ArtifactMaterialization::Build;
    let artifact_plan = artifact_import_plan(&build_plan, &bundle)?;
    let closure: ImplementationClosureBundle =
        read_canonical(closure_path, "could not read implementation closure")?;
    let platform: SupportedNativePlatformSet = read_canonical(
        platform_path,
        "could not read supported native-platform set",
    )?;
    let platform_matrix_digest = platform.observation_set_digest.clone();
    let profile: SignoffHostProfile =
        read_canonical(host_profile_path, "could not read sign-off host profile")?;
    let preflight: HostPreflightRecord =
        read_canonical(preflight_path, "could not read sign-off host preflight")?;
    let (plan, policy) =
        assemble_checked_run_plan(root, artifact_plan, closure, platform, profile, preflight)?;
    if plan != supplied_plan {
        return Err(BinaryError::Operational(
            "supplied sign-off plan differs from the Rust-reconstructed authoritative plan",
        ));
    }
    let run_plan_digest = signoff_run_plan_digest(&plan, &policy.catalog)?;
    let routes = policy
        .routes
        .routes()
        .iter()
        .map(|(case_id, route)| {
            let case = policy
                .catalog
                .cases()
                .get(case_id)
                .expect("the compiled facade registry is total over the admitted catalog");
            FacadeAdmissionRoute {
                case_id: case_id.clone(),
                program: case.program.clone(),
                fixture_digest: case.fixture_digest.clone(),
                boundary: route.boundary().to_owned(),
                execution_selector: route.execution_selector().to_owned(),
                executed: route.executed(),
                timeout: case.timeout,
                retry: case.retry.clone(),
                network: case.network.clone(),
                concurrency_class: case.concurrency_class,
            }
        })
        .collect::<Vec<_>>();
    let route_registry_digest = canonical_digest(
        DigestDomain::ConformanceProgramRegistry,
        &("facade-admission-routes", &routes),
    )
    .map_err(|_| BinaryError::Operational("could not identify admitted facade routes"))?;
    let body = FacadeAdmissionBody {
        format_version: plan.format_version,
        run_plan_digest,
        target_digest: plan.target_digest.clone(),
        subject_revision: plan.subject_revision.clone(),
        platform: plan.platform.clone(),
        host_profile_digest: plan.host_profile_digest.clone(),
        preflight_digest: plan.preflight_digest.clone(),
        artifact_plan: plan.artifact_plan.clone(),
        artifact_bundle_digest: bundle.bundle_digest().clone(),
        artifact_manifest_digest: bundle.manifest_digest().clone(),
        artifact_payload_digest: bundle.manifest().payload_digest.clone(),
        closure_bundle_digest: plan.closure_bundle_digest.clone(),
        platform_matrix_digest,
        case_catalog_digest: plan.case_catalog_digest.clone(),
        route_registry_digest,
        network_policies: plan.network_policies.clone(),
        maximum_concurrency: plan.maximum_concurrency,
        expected_case_executions: plan.expected_case_executions,
        total_budget: plan.total_budget,
        routes,
    };
    let projection_digest = canonical_digest(
        DigestDomain::ConformancePolicy,
        &("facade-admission-projection", &body),
    )
    .map_err(|_| BinaryError::Operational("could not identify facade admission projection"))?;
    let bytes = canonical_bytes(&FacadeAdmissionProjection {
        projection_digest,
        body,
    })
    .map_err(|_| BinaryError::Operational("could not encode facade admission projection"))?;
    if bytes.len() > MAX_FACADE_ADMISSION_OUTPUT {
        return Err(BinaryError::Operational(
            "facade admission projection exceeds its fixed output bound",
        ));
    }
    write_new(output_path, &bytes)
}

#[allow(clippy::too_many_arguments)]
fn verdict(
    root: &Path,
    plan_path: &Path,
    closure_path: &Path,
    platform_path: &Path,
    security_path: &Path,
    bundle_path: &Path,
    observation_path: &Path,
    output_path: &Path,
    markdown_output_path: &Path,
) -> Result<(), BinaryError> {
    let plan: SignoffRunPlan = read_canonical(plan_path, "could not read canonical sign-off plan")?;
    let closure: ImplementationClosureBundle =
        read_canonical(closure_path, "could not read implementation closure")?;
    let platform: SupportedNativePlatformSet = read_canonical(
        platform_path,
        "could not read supported native-platform set",
    )?;
    let security: ArtifactSecurityReport =
        read_canonical(security_path, "could not read artifact security report")?;
    let policy = checked_signoff_policy(root)?;
    let catalog = &policy.catalog;
    let rebuilt_closure =
        assemble_implementation_closure_bundle(ImplementationClosureBundleInput {
            format_version: closure.format_version,
            target_digest: closure.target_digest.clone(),
            subject: closure.subject.clone(),
            child_closures: closure.child_closures.values().cloned().collect(),
            compatible_assets: closure.compatible_assets.values().cloned().collect(),
            generated_assets: closure.generated_assets.clone(),
            platform_matrix_digest: closure.platform_matrix_digest.clone(),
            rust_security_digest: closure.rust_security_digest.clone(),
            plan: closure.plan.iter().cloned().collect(),
        })?;
    let rebuilt_platform = assemble_supported_native_platform_set(
        platform.target_digest.clone(),
        platform.native_observations.values().cloned().collect(),
    )?;
    validate_artifact_security_report(&security)?;
    if rebuilt_closure != closure
        || rebuilt_platform != platform
        || closure.bundle_digest != plan.closure_bundle_digest
        || closure.platform_matrix_digest != platform.observation_set_digest
        || closure.rust_security_digest != security.rust_security_digest
        || platform.observation_set_digest != closure.platform_matrix_digest
        || platform.target_digest != plan.target_digest
        || security.artifact_manifest_digest == Digest::sha256([])
        || security.artifact_payload_digest == Digest::sha256([])
    {
        return Err(BinaryError::Operational(
            "sign-off closure platform or security evidence is stale",
        ));
    }
    let bundle_bytes = fs::read(bundle_path)
        .map_err(|_| BinaryError::Operational("could not read exact-target artifact bundle"))?;
    let bundle = decode_artifact_bundle(&bundle_bytes)?;
    let raw: RawSignoffFacadeObservation =
        read_canonical(observation_path, "could not read raw sign-off observation")?;
    if raw.artifact_import_receipt_digest != raw.artifact_import_receipt.receipt_digest {
        return Err(BinaryError::Operational(
            "raw sign-off observation substituted its artifact import receipt",
        ));
    }
    let admitted_artifact =
        admit_artifact_import_receipt(&plan.artifact_plan, bundle, &raw.artifact_import_receipt)?;
    if security.artifact_manifest_digest != *admitted_artifact.manifest_digest()
        || security.artifact_payload_digest != *admitted_artifact.payload_digest()
        || security.artifact_import_receipt_digest != raw.artifact_import_receipt_digest
    {
        return Err(BinaryError::Operational(
            "security report does not cover the retained artifact bytes",
        ));
    }
    let admitted = admit_signoff_facade_observation(
        &plan,
        catalog,
        &policy.routes,
        &admitted_artifact,
        &platform.observation_set_digest,
        &security,
        raw,
    )?;
    let context = SignoffAdmissionContext {
        run_plan: &plan,
        case_catalog: catalog,
        case_bindings: admitted.bindings(),
        artifact_manifest_digest: admitted_artifact.manifest_digest(),
        artifact_payload_digest: admitted_artifact.payload_digest(),
        artifact_import_receipt: &admitted.observation().artifact_import_receipt,
        platform_matrix_digest: &platform.observation_set_digest,
        security_report_digest: &security.report_digest,
        engine_identity_digest: &admitted.baseline().engine.identity_digest,
        baseline_digest: &admitted.baseline().baseline_digest,
        stable_connector: admitted.stable_connector(),
    };
    let result = derive_atomic_signoff_verdict(&context, admitted.observation().clone());
    write_new(
        output_path,
        &encode_atomic_signoff_verdict(&result)
            .map_err(|_| BinaryError::Operational("could not encode atomic sign-off verdict"))?,
    )?;
    write_new(
        markdown_output_path,
        render_atomic_signoff_verdict(&result).as_bytes(),
    )?;
    match result.decision {
        VerdictDecision::Passed { .. } => Ok(()),
        VerdictDecision::Failed { diagnostics } => Err(BinaryError::Contract(diagnostics)),
    }
}

fn handoff(
    bundle_path: &Path,
    verdict_path: &Path,
    output_path: &Path,
    markdown_output_path: &Path,
) -> Result<(), BinaryError> {
    let bundle_bytes = fs::read(bundle_path)
        .map_err(|_| BinaryError::Operational("could not read retained sign-off bundle"))?;
    let bundle = decode_artifact_bundle(&bundle_bytes)?;
    let verdict_bytes = fs::read(verdict_path)
        .map_err(|_| BinaryError::Operational("could not read atomic sign-off verdict"))?;
    let verdict = decode_atomic_signoff_verdict(&verdict_bytes)
        .map_err(|_| BinaryError::Operational("atomic sign-off verdict is invalid"))?;
    let record = derive_release_handoff(&bundle, &verdict)?;
    write_new(
        output_path,
        &encode_release_handoff(&record)
            .map_err(|_| BinaryError::Operational("could not encode release handoff"))?,
    )?;
    write_new(
        markdown_output_path,
        render_release_handoff(&record).as_bytes(),
    )
}

struct CheckedSignoffPolicy {
    catalog: CaseCatalog,
    routes: dagger_sdk_completeness::FacadeRouteRegistry,
}

fn rust_security_report(root: &Path, output_path: &Path) -> Result<(), BinaryError> {
    let observation = reviewed_rust_dependency_security_observation();
    if observation
        .cargo_roots
        .iter()
        .any(|cargo| !root.join(cargo.manifest.as_str()).is_file())
        || observation
            .committed_lockfiles
            .iter()
            .any(|lockfile| !root.join(lockfile.as_str()).is_file())
    {
        return Err(BinaryError::Operational(
            "reviewed Rust security root or lockfile is absent",
        ));
    }
    let report = admit_rust_dependency_security(observation)?;
    let bytes = canonical_bytes(&report)
        .map_err(|_| BinaryError::Operational("could not encode Rust security report"))?;
    write_new(output_path, &bytes)
}

fn packaged_scan(
    root: &Path,
    seed_path: &Path,
    cli_digest: &str,
    backend_digest: &str,
    frontend_digest: &str,
    output_path: &Path,
) -> Result<(), BinaryError> {
    let seed_bytes = read_bounded_file(
        seed_path,
        MAX_CANARY_SEED_TEXT,
        "could not read ephemeral canary seed",
        "ephemeral canary seed exceeds its byte bound",
    )?;
    let seed_text = String::from_utf8(seed_bytes)
        .map_err(|_| BinaryError::Operational("ephemeral canary seed is not UTF-8"))?;
    let seed = decode_canary_seed(seed_text.trim())?;
    let canaries = secret_canary_set_from_entropy(&seed)
        .map_err(|_| BinaryError::Operational("ephemeral canary seed is invalid"))?;
    let parse_digest = |value: &str| {
        Digest::from_str(value).map_err(|_| {
            BinaryError::Operational("packaged artifact identity is not canonical SHA-256")
        })
    };
    let artifacts = [
        (
            "build/cli",
            PackagedArtifactKind::RawExecutable,
            parse_digest(cli_digest)?,
        ),
        (
            "build/backend-image.tar",
            PackagedArtifactKind::OciImageTar,
            parse_digest(backend_digest)?,
        ),
        (
            "build/frontend-image.tar",
            PackagedArtifactKind::OciImageTar,
            parse_digest(frontend_digest)?,
        ),
    ];
    let mut reports = Vec::with_capacity(artifacts.len());
    for (path, kind, expected_digest) in artifacts {
        let artifact_path = RepositoryRelativePath::new(path)
            .map_err(|_| BinaryError::Operational("packaged artifact path is invalid"))?;
        let mut file = fs::File::open(root.join(path))
            .map_err(|_| BinaryError::Operational("packaged artifact is absent"))?;
        reports.push(scan_packaged_artifact(
            &mut file,
            artifact_path,
            kind,
            &expected_digest,
            &canaries,
        )?);
    }
    let bundle = assemble_packaged_artifact_scan_bundle(reports)?;
    let bytes = canonical_bytes(&bundle)
        .map_err(|_| BinaryError::Operational("could not encode packaged artifact scan"))?;
    write_new(output_path, &bytes)
}

fn secret_report(
    root: &Path,
    seed_path: &Path,
    packaged_scan_path: &Path,
    output_path: &Path,
) -> Result<(), BinaryError> {
    let seed_bytes = read_bounded_file(
        seed_path,
        MAX_CANARY_SEED_TEXT,
        "could not read ephemeral canary seed",
        "ephemeral canary seed exceeds its byte bound",
    )?;
    let seed_text = String::from_utf8(seed_bytes)
        .map_err(|_| BinaryError::Operational("ephemeral canary seed is not UTF-8"))?;
    let seed = decode_canary_seed(seed_text.trim())?;
    let canaries = secret_canary_set_from_entropy(&seed)
        .map_err(|_| BinaryError::Operational("ephemeral canary seed is invalid"))?;
    let sensitive =
        SensitiveIdentitySet::new([seed.to_vec(), seed_text.trim().as_bytes().to_vec()])
            .map_err(|_| BinaryError::Operational("ephemeral sensitive identity is invalid"))?;
    let domains = [
        SecretInspectionDomain::SourceFiles,
        SecretInspectionDomain::GeneratedAndPackagedFiles,
        SecretInspectionDomain::ArtifactEntries,
        SecretInspectionDomain::CacheAndProvenance,
        SecretInspectionDomain::ProcessOutput,
        SecretInspectionDomain::ErrorsAndDebug,
        SecretInspectionDomain::DiagnosticsAndTraces,
        SecretInspectionDomain::Reports,
        SecretInspectionDomain::DraftVerdict,
    ];
    let mut inspections = Vec::with_capacity(domains.len());
    let mut retained = BTreeMap::new();
    for domain in domains {
        let slug = secret_domain_slug(domain);
        let bytes = read_bounded_file(
            &root.join(format!("{slug}.evidence")),
            secret_evidence_domain_byte_limit(domain),
            "secret inspection domain is absent",
            "secret inspection domain exceeds its declared byte bound",
        )?;
        if bytes.is_empty()
            || contains_bytes(&bytes, &seed)
            || contains_bytes(&bytes, seed_text.trim().as_bytes())
        {
            return Err(BinaryError::Operational(
                "secret inspection domain contains an ephemeral seed identity",
            ));
        }
        let coordinate = RepositoryRelativePath::new(format!("signoff/{slug}"))
            .map_err(|_| BinaryError::Operational("secret inspection coordinate is invalid"))?;
        inspections.push(inspect_canary_chunks(
            &canaries,
            domain,
            coordinate,
            [bytes.as_slice()],
        )?);
        retained.insert(domain, bytes);
    }
    let sanitized_outputs = [
        SecretInspectionDomain::Reports,
        SecretInspectionDomain::DraftVerdict,
    ]
    .into_iter()
    .map(|domain| {
        sanitize_durable_evidence(
            retained
                .get(&domain)
                .expect("every fixed secret domain was read"),
            &canaries,
            &sensitive,
        )
    })
    .collect::<Result<Vec<_>, _>>()?;
    let packaged_artifacts: PackagedArtifactScanBundle =
        read_canonical(packaged_scan_path, "could not read packaged artifact scan")?;
    let rebuilt_packaged_artifacts =
        assemble_packaged_artifact_scan_bundle(packaged_artifacts.artifacts.iter().cloned())?;
    if rebuilt_packaged_artifacts != packaged_artifacts {
        return Err(BinaryError::Operational(
            "packaged artifact scan is stale or identity-inconsistent",
        ));
    }
    let report = admit_secret_evidence(SecretEvidenceInput {
        canary_set_digest: canaries.digest().clone(),
        inspections,
        sanitized_outputs,
        packaged_artifacts,
        artifact_credentials_absent: true,
        verdict_credentials_absent: true,
        redaction_proven: true,
    })?;
    let bytes = canonical_bytes(&report)
        .map_err(|_| BinaryError::Operational("could not encode secret evidence report"))?;
    write_new(output_path, &bytes)
}

fn implementation_closure(
    root: &Path,
    platform_path: &Path,
    rust_security_path: &Path,
    output_path: &Path,
    markdown_output_path: &Path,
) -> Result<(), BinaryError> {
    let platform: SupportedNativePlatformSet = read_canonical(
        platform_path,
        "could not read supported native-platform set",
    )?;
    let rebuilt_platform = assemble_supported_native_platform_set(
        platform.target_digest.clone(),
        platform.native_observations.values().cloned().collect(),
    )?;
    if rebuilt_platform != platform {
        return Err(BinaryError::Operational(
            "supported native-platform set is not the current canonical result",
        ));
    }
    let security: RustDependencySecurityReport = read_canonical(
        rust_security_path,
        "could not read ordinary Rust security report",
    )?;
    let expected_security =
        admit_rust_dependency_security(reviewed_rust_dependency_security_observation())?;
    if security != expected_security {
        return Err(BinaryError::Operational(
            "ordinary Rust security report is stale",
        ));
    }
    let scope: ReviewedConformanceScope = read_canonical(
        &root.join("sdk/rust/completeness/conformance-scope.json"),
        "could not read reviewed conformance scope",
    )?;
    if platform.target_digest != scope.target_digest {
        return Err(BinaryError::Operational(
            "supported native-platform set names a different exact target",
        ));
    }
    let subject = SubjectIdentity::SourceDigest(
        rust_artifact_digest(root)
            .map_err(|_| BinaryError::Operational("could not identify current Rust source"))?,
    );
    let generated_assets = BTreeMap::from([
        (
            GeneratedAssetDomain::CoreBindings,
            closure_path_digest(
                root,
                "core-bindings",
                &["sdk/rust/crates/dagger-sdk/src/gen"],
            )?,
        ),
        (
            GeneratedAssetDomain::EnginePackage,
            closure_path_digest(
                root,
                "engine-package",
                &["sdk/rust/runtime", "sdk/rust/crates/dagger-sdk-engine"],
            )?,
        ),
        (
            GeneratedAssetDomain::ModuleAssets,
            closure_path_digest(
                root,
                "module-assets",
                &[
                    "sdk/rust/crates/dagger-codegen/src/module",
                    "sdk/rust/crates/dagger-sdk/src/module",
                ],
            )?,
        ),
        (
            GeneratedAssetDomain::StandaloneClientAssets,
            closure_path_digest(
                root,
                "standalone-client-assets",
                &[
                    "sdk/rust/crates/dagger-codegen/src/client",
                    "sdk/rust/completeness/evidence/client-generation-closure.json",
                ],
            )?,
        ),
    ]);
    let child_closures = [
        closure_child(
            root,
            &scope.target_digest,
            &subject,
            &generated_assets,
            ClosureChildSpec {
                child: ChildClosure::Transport,
                evidence_format: ChildEvidenceFormat::TransportObservationRegistry,
                paths: &[
                    "sdk/rust/completeness/evidence/transport-observations.json",
                    "sdk/rust/crates/dagger-sdk/src/connector.rs",
                    "sdk/rust/crates/dagger-sdk/src/connection.rs",
                ],
                owned_assets: &[],
            },
        )?,
        closure_child(
            root,
            &scope.target_digest,
            &subject,
            &generated_assets,
            ClosureChildSpec {
                child: ChildClosure::ClientLifecycle,
                evidence_format: ChildEvidenceFormat::ClientEvidenceRegistry,
                paths: &[
                    "sdk/rust/completeness/evidence/registry.json",
                    "sdk/rust/crates/dagger-sdk/src/lifecycle.rs",
                    "sdk/rust/crates/dagger-sdk/src/launch.rs",
                    "sdk/rust/crates/dagger-sdk/src/provision.rs",
                    "sdk/rust/crates/dagger-sdk/src/session_startup.rs",
                ],
                owned_assets: &[],
            },
        )?,
        closure_child(
            root,
            &scope.target_digest,
            &subject,
            &generated_assets,
            ClosureChildSpec {
                child: ChildClosure::CoreCodegen,
                evidence_format: ChildEvidenceFormat::CoreCodegenEvidenceRegistry,
                paths: &[
                    "sdk/rust/completeness/evidence/core-codegen-registry.json",
                    "sdk/rust/completeness/artifacts/client-generation-report.json",
                    "sdk/rust/crates/dagger-sdk/src/gen",
                ],
                owned_assets: &[GeneratedAssetDomain::CoreBindings],
            },
        )?,
        closure_child(
            root,
            &scope.target_digest,
            &subject,
            &generated_assets,
            ClosureChildSpec {
                child: ChildClosure::EngineIntegration,
                evidence_format: ChildEvidenceFormat::EngineIntegrationImplementationClosure,
                paths: &[
                    "sdk/rust/completeness/engine-integration-mappings.json",
                    "sdk/rust/crates/dagger-sdk-engine",
                    "sdk/rust/runtime",
                ],
                owned_assets: &[GeneratedAssetDomain::EnginePackage],
            },
        )?,
        closure_child(
            root,
            &scope.target_digest,
            &subject,
            &generated_assets,
            ClosureChildSpec {
                child: ChildClosure::ModuleAuthoring,
                evidence_format: ChildEvidenceFormat::ModuleAuthoringImplementationClosure,
                paths: &[
                    "sdk/rust/crates/dagger-codegen/src/module",
                    "sdk/rust/crates/dagger-sdk/src/module",
                    "sdk/rust/crates/dagger-sdk-macros/src",
                ],
                owned_assets: &[GeneratedAssetDomain::ModuleAssets],
            },
        )?,
        closure_child(
            root,
            &scope.target_digest,
            &subject,
            &generated_assets,
            ClosureChildSpec {
                child: ChildClosure::StandaloneClient,
                evidence_format: ChildEvidenceFormat::ClientGenerationClosure,
                paths: &[
                    "sdk/rust/completeness/evidence/client-generation-closure.json",
                    "sdk/rust/crates/dagger-codegen/src/client",
                ],
                owned_assets: &[GeneratedAssetDomain::StandaloneClientAssets],
            },
        )?,
    ];
    let bundle = assemble_implementation_closure_bundle(ImplementationClosureBundleInput {
        format_version: scope.format_version,
        target_digest: scope.target_digest,
        subject,
        child_closures: child_closures.into_iter().collect(),
        compatible_assets: Vec::new(),
        generated_assets,
        platform_matrix_digest: platform.observation_set_digest,
        rust_security_digest: security.security_digest,
        plan: expected_closure_plan().into_inner(),
    })?;
    let bytes = canonical_bytes(&bundle)
        .map_err(|_| BinaryError::Operational("could not encode implementation closure"))?;
    write_new(output_path, &bytes)?;
    write_new(
        markdown_output_path,
        render_implementation_closure_bundle(&bundle).as_bytes(),
    )
}

struct ClosureChildSpec<'a> {
    child: ChildClosure,
    evidence_format: ChildEvidenceFormat,
    paths: &'a [&'a str],
    owned_assets: &'a [GeneratedAssetDomain],
}

fn closure_child(
    root: &Path,
    target_digest: &TargetDigest,
    subject: &SubjectIdentity,
    generated_assets: &BTreeMap<GeneratedAssetDomain, Digest>,
    spec: ClosureChildSpec<'_>,
) -> Result<ChildClosureReference, BinaryError> {
    let closure_digest = closure_path_digest(root, closure_child_slug(spec.child), spec.paths)?;
    Ok(ChildClosureReference {
        child: spec.child,
        evidence_format: spec.evidence_format,
        target_digest: target_digest.clone(),
        subject_binding: ClosureSubjectBinding::Subject {
            identity: subject.clone(),
        },
        closure_digest,
        engine_free: true,
        outcome: ClosureOutcome::Passed,
        generated_assets: spec
            .owned_assets
            .iter()
            .map(|domain| (*domain, generated_assets[domain].clone()))
            .collect(),
    })
}

fn closure_child_slug(child: ChildClosure) -> &'static str {
    match child {
        ChildClosure::Transport => "transport",
        ChildClosure::ClientLifecycle => "client-lifecycle",
        ChildClosure::CoreCodegen => "core-codegen",
        ChildClosure::EngineIntegration => "engine-integration",
        ChildClosure::ModuleAuthoring => "module-authoring",
        ChildClosure::StandaloneClient => "standalone-client",
    }
}

#[derive(Serialize)]
struct ClosureFileIdentity {
    path: RepositoryRelativePath,
    digest: Digest,
}

fn closure_path_digest(
    root: &Path,
    label: &str,
    relative_paths: &[&str],
) -> Result<Digest, BinaryError> {
    let mut files = Vec::new();
    for relative in relative_paths {
        collect_closure_files(root, Path::new(relative), &mut files)?;
    }
    files.sort_by(|left, right| left.path.cmp(&right.path));
    if files.is_empty() {
        return Err(BinaryError::Operational(
            "implementation closure evidence set is empty",
        ));
    }
    canonical_digest(DigestDomain::ConformanceClosureBundle, &(label, files))
        .map_err(|_| BinaryError::Operational("could not identify implementation closure evidence"))
}

fn collect_closure_files(
    root: &Path,
    relative: &Path,
    files: &mut Vec<ClosureFileIdentity>,
) -> Result<(), BinaryError> {
    let path = root.join(relative);
    let metadata = fs::symlink_metadata(&path)
        .map_err(|_| BinaryError::Operational("implementation closure evidence is absent"))?;
    if metadata.file_type().is_symlink() {
        return Err(BinaryError::Operational(
            "implementation closure evidence contains a symlink",
        ));
    }
    if metadata.is_dir() {
        let mut entries = fs::read_dir(&path)
            .map_err(|_| BinaryError::Operational("could not enumerate closure evidence"))?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| BinaryError::Operational("could not enumerate closure evidence"))?;
        entries.sort_by_key(std::fs::DirEntry::file_name);
        for entry in entries {
            collect_closure_files(root, &relative.join(entry.file_name()), files)?;
        }
    } else if metadata.is_file() {
        let logical = RepositoryRelativePath::new(relative.to_string_lossy().replace('\\', "/"))
            .map_err(|_| BinaryError::Operational("closure evidence path is invalid"))?;
        let bytes = fs::read(path)
            .map_err(|_| BinaryError::Operational("could not read closure evidence"))?;
        files.push(ClosureFileIdentity {
            path: logical,
            digest: Digest::sha256(bytes),
        });
    } else {
        return Err(BinaryError::Operational(
            "implementation closure evidence is not a regular file",
        ));
    }
    Ok(())
}

fn decode_canary_seed(value: &str) -> Result<[u8; 32], BinaryError> {
    if value.len() != 64 {
        return Err(BinaryError::Operational(
            "ephemeral canary seed must be 32 lowercase-hex bytes",
        ));
    }
    let mut output = [0_u8; 32];
    for (index, pair) in value.as_bytes().chunks_exact(2).enumerate() {
        let high = lower_hex_nibble(pair[0])?;
        let low = lower_hex_nibble(pair[1])?;
        output[index] = (high << 4) | low;
    }
    Ok(output)
}

fn lower_hex_nibble(value: u8) -> Result<u8, BinaryError> {
    match value {
        b'0'..=b'9' => Ok(value - b'0'),
        b'a'..=b'f' => Ok(value - b'a' + 10),
        _ => Err(BinaryError::Operational(
            "ephemeral canary seed must be 32 lowercase-hex bytes",
        )),
    }
}

fn secret_domain_slug(domain: SecretInspectionDomain) -> &'static str {
    match domain {
        SecretInspectionDomain::SourceFiles => "source-files",
        SecretInspectionDomain::GeneratedAndPackagedFiles => "generated-and-packaged-files",
        SecretInspectionDomain::ArtifactEntries => "artifact-entries",
        SecretInspectionDomain::CacheAndProvenance => "cache-and-provenance",
        SecretInspectionDomain::ProcessOutput => "process-output",
        SecretInspectionDomain::ErrorsAndDebug => "errors-and-debug",
        SecretInspectionDomain::DiagnosticsAndTraces => "diagnostics-and-traces",
        SecretInspectionDomain::Reports => "reports",
        SecretInspectionDomain::DraftVerdict => "draft-verdict",
    }
}

fn contains_bytes(haystack: &[u8], needle: &[u8]) -> bool {
    !needle.is_empty()
        && haystack
            .windows(needle.len())
            .any(|window| window == needle)
}

fn checked_signoff_policy(root: &Path) -> Result<CheckedSignoffPolicy, BinaryError> {
    let completeness = root.join("sdk/rust/completeness");
    let ledger: ResolvedLedger = read_canonical(
        &completeness.join("artifacts/ledger.json"),
        "could not read resolved completeness ledger",
    )?;
    let reviewed: ReviewedConformanceScope = read_canonical(
        &completeness.join("conformance-scope.json"),
        "could not read reviewed conformance scope",
    )?;
    let applicability: ConformanceScopeInput = read_canonical(
        &completeness.join("conformance-applicability.json"),
        "could not read conformance applicability",
    )?;
    let scope = derive_conformance_scope(&ledger, &reviewed, applicability)?;
    let assertion_input: AssertionCatalogInput = read_canonical(
        &completeness.join("conformance-assertions.json"),
        "could not read conformance assertions",
    )?;
    let fixture_input: FixtureRegistryInput = read_canonical(
        &completeness.join("conformance-fixtures.json"),
        "could not read conformance fixtures",
    )?;
    let case_input: CaseCatalogInput = read_canonical(
        &completeness.join("conformance-cases.json"),
        "could not read conformance cases",
    )?;
    let assertions = compile_assertion_catalog(&scope, assertion_input)?;
    let fixtures = compile_fixture_registry(fixture_input)?;
    let catalog = compile_case_catalog(&scope, &assertions, &fixtures, case_input)?;
    let observable = compile_observable_fixture_program_registry(&assertions, &fixtures, &catalog)?;
    let scenario_candidates: RustFirstConformanceManifestInput = read_canonical(
        &completeness.join("conformance-scenario-candidates.json"),
        "could not read Rust scenario candidates",
    )?;
    let scenario_input: RustScenarioRegistryInput = read_canonical(
        &completeness.join("conformance-scenario-realizations.json"),
        "could not read Rust scenario realizations",
    )?;
    let runner_source =
        fs::read(root.join("toolchains/rust-sdk-dev/testdata/scenario_conformance.rs"))
            .map_err(|_| BinaryError::Operational("could not read checked Rust scenario runner"))?;
    let scenarios = compile_rust_scenario_registry(
        scenario_input,
        &scenario_candidates,
        &catalog,
        &Digest::sha256(runner_source),
    )?;
    let routes = compile_facade_route_registry(&catalog, &observable, &scenarios)?;
    Ok(CheckedSignoffPolicy { catalog, routes })
}

#[allow(clippy::too_many_arguments)]
fn scanner_translate(
    root: &Path,
    findings_path: &Path,
    scanner_version_path: &Path,
    database_metadata_path: &Path,
    database_checksums_path: &Path,
    database_artifact_digest: &str,
    payload_checksum_path: &Path,
    elapsed_millis: u64,
    output_path: &Path,
) -> Result<(), BinaryError> {
    let provenance: ExternalProvenanceRegistry = read_canonical(
        &root.join("sdk/rust/completeness/security-provenance.json"),
        "could not read checked security provenance",
    )?;
    let findings = read_bounded_file(
        findings_path,
        16 * 1024 * 1024,
        "could not read Trivy findings",
        "Trivy findings exceed their byte bound",
    )?;
    let scanner_version = read_bounded_file(
        scanner_version_path,
        16 * 1024 * 1024,
        "could not read Trivy version",
        "Trivy version exceeds its byte bound",
    )?;
    let database_metadata = read_bounded_file(
        database_metadata_path,
        16 * 1024 * 1024,
        "could not read Trivy database metadata",
        "Trivy database metadata exceeds its byte bound",
    )?;
    let database_checksums = read_bounded_file(
        database_checksums_path,
        1024,
        "could not read Trivy database checksums",
        "Trivy database checksums exceed their byte bound",
    )?;
    let payload_checksum = String::from_utf8(read_bounded_file(
        payload_checksum_path,
        1024,
        "could not read scanned payload checksum",
        "scanned payload checksum exceeds its byte bound",
    )?)
    .map_err(|_| BinaryError::Operational("scanned payload checksum is not UTF-8"))?;
    let database_artifact_digest = Digest::from_str(database_artifact_digest).map_err(|_| {
        BinaryError::Operational("Trivy database artifact digest is not canonical SHA-256")
    })?;
    let observation = translate_trivy_artifact_scan(
        &findings,
        &scanner_version,
        &database_metadata,
        &database_checksums,
        database_artifact_digest,
        &payload_checksum,
        elapsed_millis,
        &provenance,
    )?;
    let bytes = canonical_bytes(&observation)
        .map_err(|_| BinaryError::Operational("could not encode scanner observation"))?;
    write_new(output_path, &bytes)
}

#[allow(clippy::too_many_arguments)]
fn security_report(
    root: &Path,
    plan_path: &Path,
    bundle_path: &Path,
    rust_security_path: &Path,
    observation_path: &Path,
    policy_date: &str,
    output_path: &Path,
) -> Result<(), BinaryError> {
    let started = Instant::now();
    let plan = read_artifact_plan(plan_path)?;
    if !matches!(plan.materialization, ArtifactMaterialization::Import { .. }) {
        return Err(BinaryError::Operational(
            "security-report requires the authoritative Import strategy",
        ));
    }
    let bundle_bytes = fs::read(bundle_path)
        .map_err(|_| BinaryError::Operational("could not read exact-target artifact bundle"))?;
    let bundle = decode_artifact_bundle(&bundle_bytes)?;
    let raw: RawSignoffFacadeObservation =
        read_canonical(observation_path, "could not read raw sign-off observation")?;
    if raw.artifact_import_receipt_digest != raw.artifact_import_receipt.receipt_digest {
        return Err(BinaryError::Operational(
            "raw sign-off observation substituted its artifact import receipt",
        ));
    }
    let admitted = admit_artifact_import_receipt(&plan, bundle, &raw.artifact_import_receipt)?;
    let rust_security: RustDependencySecurityReport = read_canonical(
        rust_security_path,
        "could not read ordinary Rust security closure",
    )?;
    validate_secret_evidence_report(&raw.secret_report)?;
    if raw.artifact_manifest_digest != *admitted.manifest_digest()
        || raw.artifact_payload_digest != *admitted.payload_digest()
        || raw.scanner_observation.payload_digest != *admitted.payload_digest()
        || raw.scanner_result_digest != raw.scanner_observation.scanner_result_digest
    {
        return Err(BinaryError::Operational(
            "scanner observation does not cover the admitted retained artifact",
        ));
    }
    let elapsed = u64::try_from(started.elapsed().as_millis())
        .unwrap_or(u64::MAX)
        .max(1);
    let policy_elapsed = NonZeroMillis::new(elapsed)
        .map_err(|_| BinaryError::Operational("security policy timing exceeded its bound"))?;
    let registry: ExternalProvenanceRegistry = read_canonical(
        &root.join("sdk/rust/completeness/security-provenance.json"),
        "could not read checked security provenance",
    )?;
    let context = ExceptionEvaluationContext {
        current_date: UtcDate::new(policy_date)
            .map_err(|_| BinaryError::Operational("policy date is invalid"))?,
        target_revision: plan.target_revision.clone(),
        fixed_versions: BTreeMap::new(),
        withdrawn_advisories: CanonicalSet::default(),
    };
    let report = admit_artifact_security(
        &admitted,
        &rust_security,
        &registry,
        &context,
        ArtifactSecurityObservation {
            scanner: raw.scanner_observation,
            exceptions: Vec::new(),
            secret_report: raw.secret_report,
            policy_elapsed,
        },
    )?;
    let bytes = canonical_bytes(&report)
        .map_err(|_| BinaryError::Operational("could not encode artifact security report"))?;
    write_new(output_path, &bytes)
}

fn read_canonical<T: serde::de::DeserializeOwned + serde::Serialize>(
    path: &Path,
    message: &'static str,
) -> Result<T, BinaryError> {
    let bytes = fs::read(path).map_err(|_| BinaryError::Operational(message))?;
    decode_canonical(&bytes).map_err(|_| BinaryError::Operational(message))
}

fn path_argument(name: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .required(true)
        .value_parser(value_parser!(PathBuf))
}

fn required_path<'a>(values: &'a clap::ArgMatches, name: &str) -> &'a PathBuf {
    values
        .get_one::<PathBuf>(name)
        .expect("clap requires every path argument")
}

fn artifact_seed(root: &Path, repository: &str, output_path: &Path) -> Result<(), BinaryError> {
    if !dagger_sdk_completeness::is_canonical_subject_repository(repository) {
        return Err(BinaryError::Operational(
            "artifact subject repository is not canonical credential-free HTTPS",
        ));
    }
    let revision = git_output(root, &["rev-parse", "HEAD"])?;
    let revision = CommitSha::new(revision.trim())
        .map_err(|_| BinaryError::Operational("artifact subject is not a full immutable commit"))?;
    if !git_output(root, &["status", "--porcelain", "--untracked-files=all"])?.is_empty() {
        return Err(BinaryError::Operational(
            "artifact subject workspace is not clean",
        ));
    }
    let reachable = Command::new("git")
        .arg("-C")
        .arg(root)
        .args(["cat-file", "-e"])
        .arg(format!("{}^{{commit}}", revision.as_str()))
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map_err(|_| BinaryError::Operational("could not validate artifact subject commit"))?
        .success();
    if !reachable {
        return Err(BinaryError::Operational(
            "artifact subject commit is not reachable",
        ));
    }

    let target: TargetDescriptor = read_canonical(
        &root.join("sdk/rust/completeness/target.json"),
        "could not read checked Rust SDK target",
    )?;
    let scope: ReviewedConformanceScope = read_canonical(
        &root.join("sdk/rust/completeness/conformance-scope.json"),
        "could not read reviewed conformance scope",
    )?;
    let registry: ExternalProvenanceRegistry = read_canonical(
        &root.join("sdk/rust/completeness/security-provenance.json"),
        "could not read checked security provenance",
    )?;
    let provenance_id = |role| {
        registry
            .records
            .get(&role)
            .map(|record| record.id.clone())
            .ok_or(BinaryError::Operational(
                "checked security provenance is incomplete",
            ))
    };
    let toolchain_digest = |role| {
        registry
            .records
            .get(&role)
            .map(|record| record.immutable_digest.clone())
            .ok_or(BinaryError::Operational(
                "checked security provenance is incomplete",
            ))
    };
    let rust_source_digest = rust_artifact_digest(root)
        .map_err(|_| BinaryError::Operational("could not identify focused Rust source"))?;
    let go_toolchain = toolchain_digest(ExternalInputRole::GoToolchain)?;
    let seed = ArtifactPlanSeed {
        format_version: scope.format_version,
        target_descriptor_digest: scope.target_digest,
        target_revision: target.dagger_revision.clone(),
        subject: dagger_sdk_completeness::SubjectRevisionObservation {
            repository: repository.to_owned(),
            revision,
            focused_source_digest: rust_source_digest.clone(),
            workspace_focused_source_digest: rust_source_digest,
            reachable,
            clean: true,
            immutable: true,
        },
        platform: PlatformDescriptor::linux_amd64(),
        engine_input_digest: closure_path_digest(
            root,
            "artifact-engine-input",
            &[
                "cmd/engine",
                "engine",
                "core",
                "dagql",
                "go.mod",
                "go.sum",
                "toolchains/engine-dev",
                "toolchains/go",
            ],
        )?,
        cli_input_digest: closure_path_digest(
            root,
            "artifact-cli-input",
            &[
                "cmd/dagger",
                "cmd/codegen",
                "go.mod",
                "go.sum",
                "toolchains/cli-dev",
                "toolchains/go",
            ],
        )?,
        go_runtime_digest: canonical_digest(
            DigestDomain::Artifact,
            &(
                "focused-go-runtime",
                &target.dagger_revision,
                &target.schema_digest,
                &go_toolchain,
            ),
        )
        .map_err(|_| BinaryError::Operational("could not identify Go runtime inputs"))?,
        rust_manifest_digest: closure_path_digest(
            root,
            "artifact-rust-manifests",
            &[
                "sdk/rust/Cargo.toml",
                "sdk/rust/Cargo.lock",
                "sdk/rust/rust-toolchain.toml",
            ],
        )?,
        toolchain_digests: BTreeMap::from([
            (
                ToolchainRole::ArtifactBuilder,
                toolchain_digest(ExternalInputRole::ArtifactBuilderImage)?,
            ),
            (
                ToolchainRole::EngineBase,
                toolchain_digest(ExternalInputRole::EngineBaseImage)?,
            ),
            (
                ToolchainRole::RustToolchain,
                toolchain_digest(ExternalInputRole::RustToolchain)?,
            ),
            (ToolchainRole::GoToolchain, go_toolchain),
            (
                ToolchainRole::ArtifactScanner,
                toolchain_digest(ExternalInputRole::ScannerImage)?,
            ),
        ]),
        component_provenance: BTreeMap::from([
            (
                ArtifactComponent::Engine,
                CanonicalSet::new([
                    provenance_id(ExternalInputRole::EngineBaseImage)?,
                    provenance_id(ExternalInputRole::GoToolchain)?,
                ]),
            ),
            (
                ArtifactComponent::Cli,
                CanonicalSet::new([provenance_id(ExternalInputRole::GoToolchain)?]),
            ),
            (
                ArtifactComponent::GoRuntime,
                CanonicalSet::new([provenance_id(ExternalInputRole::GoToolchain)?]),
            ),
            (
                ArtifactComponent::RustSdk,
                CanonicalSet::new([
                    provenance_id(ExternalInputRole::ArtifactBuilderImage)?,
                    provenance_id(ExternalInputRole::RustToolchain)?,
                ]),
            ),
        ]),
    };
    let bytes = canonical_bytes(&seed)
        .map_err(|_| BinaryError::Operational("could not encode artifact plan seed"))?;
    write_new(output_path, &bytes)
}

#[allow(clippy::too_many_arguments)]
fn artifact_plan(
    seed_path: &Path,
    engine_content_digest: &str,
    cli_content_digest: &str,
    go_runtime_content_digest: &str,
    rust_content_digest: &str,
    rust_descriptor_digest: &str,
    rust_dependency_descriptor_path: &Path,
    rust_dependency_descriptor_digest: &str,
    output_path: &Path,
) -> Result<(), BinaryError> {
    let seed: ArtifactPlanSeed =
        read_canonical(seed_path, "could not read canonical artifact plan seed")?;
    let parse_digest = |value: &str| {
        Digest::from_str(value)
            .map_err(|_| BinaryError::Operational("component identity is not canonical SHA-256"))
    };
    let dependency_bytes = fs::read(rust_dependency_descriptor_path)
        .map_err(|_| BinaryError::Operational("could not read Rust dependency descriptor"))?;
    if dependency_bytes.is_empty() || dependency_bytes.len() > 4096 {
        return Err(BinaryError::Operational(
            "Rust dependency descriptor exceeds its byte bound",
        ));
    }
    let rust_dependency: RustSdkDependencyDescriptor = serde_json::from_slice(&dependency_bytes)
        .map_err(|_| BinaryError::Operational("could not decode Rust dependency descriptor"))?;
    if serde_json::to_vec(&rust_dependency).ok().as_deref() != Some(dependency_bytes.as_slice())
        || rust_dependency.direct_digest().map_err(|_| {
            BinaryError::Operational("could not identify Rust dependency descriptor")
        })? != parse_digest(rust_dependency_descriptor_digest)?
    {
        return Err(BinaryError::Operational(
            "Rust dependency descriptor bytes or identity are not canonical",
        ));
    }
    let plan = seal_artifact_build_plan(
        seed,
        parse_digest(rust_descriptor_digest)?,
        rust_dependency,
        parse_digest(rust_dependency_descriptor_digest)?,
        BTreeMap::from([
            (
                ArtifactComponent::Engine,
                parse_digest(engine_content_digest)?,
            ),
            (ArtifactComponent::Cli, parse_digest(cli_content_digest)?),
            (
                ArtifactComponent::GoRuntime,
                parse_digest(go_runtime_content_digest)?,
            ),
            (
                ArtifactComponent::RustSdk,
                parse_digest(rust_content_digest)?,
            ),
        ]),
    )?;
    let bytes = canonical_bytes(&plan)
        .map_err(|_| BinaryError::Operational("could not encode exact-target artifact plan"))?;
    write_new(output_path, &bytes)
}

fn git_output(root: &Path, arguments: &[&str]) -> Result<String, BinaryError> {
    let output = Command::new("git")
        .arg("-C")
        .arg(root)
        .args(arguments)
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output()
        .map_err(|_| BinaryError::Operational("could not inspect artifact subject revision"))?;
    if !output.status.success() {
        return Err(BinaryError::Operational(
            "artifact subject Git inspection failed",
        ));
    }
    String::from_utf8(output.stdout)
        .map_err(|_| BinaryError::Operational("artifact subject Git output is not UTF-8"))
}

fn artifact_build(
    plan_path: &Path,
    payload_path: &Path,
    observation_path: &Path,
    bundle_output: &Path,
    manifest_output: &Path,
    receipt_output: &Path,
) -> Result<(), BinaryError> {
    let plan = read_artifact_plan(plan_path)?;
    if !matches!(plan.materialization, ArtifactMaterialization::Build) {
        return Err(BinaryError::Operational(
            "artifact-build requires the Build strategy",
        ));
    }
    let observation: ArtifactBuildObservation = read_canonical(
        observation_path,
        "could not read exact-target artifact build observation",
    )?;
    let payload = fs::read(payload_path)
        .map_err(|_| BinaryError::Operational("could not read exact-target OCI payload"))?;
    let manifest = artifact_manifest_for_payload(&plan, &payload)?;
    let provenance = artifact_provenance_document(&plan)?;
    let bundle = assemble_artifact_bundle(manifest.clone(), provenance, payload)?;
    let receipt = artifact_build_receipt(&plan, &bundle, observation)?;
    let admitted = admit_artifact_build_receipt(&plan, bundle, &receipt)?;
    write_new(bundle_output, admitted.bundle().bytes())?;
    let manifest_bytes = canonical_bytes(&manifest)
        .map_err(|_| BinaryError::Operational("could not encode exact-target manifest"))?;
    write_new(manifest_output, &manifest_bytes)?;
    let receipt_bytes = canonical_bytes(&receipt)
        .map_err(|_| BinaryError::Operational("could not encode artifact build receipt"))?;
    write_new(receipt_output, &receipt_bytes)
}

fn artifact_extract(
    plan_path: &Path,
    bundle_path: &Path,
    payload_output: &Path,
    manifest_output: &Path,
) -> Result<(), BinaryError> {
    let plan = read_artifact_plan(plan_path)?;
    if !matches!(plan.materialization, ArtifactMaterialization::Import { .. }) {
        return Err(BinaryError::Operational(
            "artifact-import requires the Import strategy",
        ));
    }
    let bytes = fs::read(bundle_path)
        .map_err(|_| BinaryError::Operational("could not read exact-target artifact bundle"))?;
    let bundle = decode_artifact_bundle(&bytes)?;
    let manifest = bundle.manifest().clone();
    verify_artifact_import_source(&plan, &bundle)?;
    write_new(payload_output, bundle.payload())?;
    let manifest_bytes = canonical_bytes(&manifest)
        .map_err(|_| BinaryError::Operational("could not encode imported artifact manifest"))?;
    write_new(manifest_output, &manifest_bytes)
}

fn artifact_import(
    plan_path: &Path,
    bundle_path: &Path,
    observation_path: &Path,
    receipt_output: &Path,
) -> Result<(), BinaryError> {
    let plan = read_artifact_plan(plan_path)?;
    if !matches!(plan.materialization, ArtifactMaterialization::Import { .. }) {
        return Err(BinaryError::Operational(
            "artifact-import requires the authoritative Import strategy",
        ));
    }
    let bytes = fs::read(bundle_path)
        .map_err(|_| BinaryError::Operational("could not read exact-target artifact bundle"))?;
    let bundle = decode_artifact_bundle(&bytes)?;
    let observation: ArtifactImportObservation = read_canonical(
        observation_path,
        "could not read exact-target artifact import observation",
    )?;
    let receipt = artifact_import_receipt(&plan, &bundle, observation)?;
    admit_artifact_import_receipt(&plan, bundle, &receipt)?;
    let receipt_bytes = canonical_bytes(&receipt)
        .map_err(|_| BinaryError::Operational("could not encode artifact import receipt"))?;
    write_new(receipt_output, &receipt_bytes)
}

fn read_artifact_plan(path: &Path) -> Result<ArtifactPlan, BinaryError> {
    let bytes = fs::read(path)
        .map_err(|_| BinaryError::Operational("could not read exact-target artifact plan"))?;
    decode_canonical(&bytes)
        .map_err(|_| BinaryError::Operational("exact-target artifact plan is not canonical"))
}

fn preflight(profile_path: &Path, output_path: &Path) -> Result<(), BinaryError> {
    let profile_bytes = fs::read(profile_path)
        .map_err(|_| BinaryError::Operational("could not read checked host profile"))?;
    let profile: SignoffHostProfile = decode_canonical(&profile_bytes)
        .map_err(|_| BinaryError::Operational("checked host profile is not canonical"))?;
    let plan = plan_host_preflight(profile)?;
    let mut probe = ProcessHostProbe::new(&plan)?;
    let result = run_host_preflight(&plan, &mut probe);
    let cleanup = probe.cleanup();
    let record = match (result, cleanup) {
        (Ok(record), Ok(())) => record,
        (Err(errors), Ok(())) => return Err(BinaryError::Contract(errors)),
        (Err(errors), Err(_)) => {
            let mut diagnostics = errors.as_slice().to_vec();
            diagnostics.push(cleanup_diagnostic());
            return Err(BinaryError::Contract(
                ConformanceDiagnosticSet::new(diagnostics)
                    .expect("primary and cleanup diagnostics are non-empty"),
            ));
        }
        (Ok(_), Err(_)) => {
            return Err(BinaryError::Contract(
                ConformanceDiagnosticSet::new([cleanup_diagnostic()])
                    .expect("cleanup diagnostic is non-empty"),
            ));
        }
    };
    write_record(output_path, &record)
}

fn write_record(path: &Path, record: &HostPreflightRecord) -> Result<(), BinaryError> {
    let bytes = canonical_bytes(record)
        .map_err(|_| BinaryError::Operational("could not encode preflight record"))?;
    write_new(path, &bytes)
}

fn read_bounded_file(
    path: &Path,
    maximum: u64,
    read_error: &'static str,
    excess_error: &'static str,
) -> Result<Vec<u8>, BinaryError> {
    let file = fs::File::open(path).map_err(|_| BinaryError::Operational(read_error))?;
    // `take` caps allocation even if an untrusted producer races file metadata or streams a
    // growing input. The single sentinel byte distinguishes the exact boundary from overflow.
    let mut reader = file.take(maximum.saturating_add(1));
    let mut bytes = Vec::new();
    reader
        .read_to_end(&mut bytes)
        .map_err(|_| BinaryError::Operational(read_error))?;
    if u64::try_from(bytes.len()).unwrap_or(u64::MAX) > maximum {
        return Err(BinaryError::Operational(excess_error));
    }
    Ok(bytes)
}

fn write_new(path: &Path, bytes: &[u8]) -> Result<(), BinaryError> {
    if path.exists() {
        return Err(BinaryError::Operational("sign-off output already exists"));
    }
    let parent = path
        .parent()
        .ok_or(BinaryError::Operational("sign-off output has no parent"))?;
    fs::create_dir_all(parent)
        .map_err(|_| BinaryError::Operational("could not create preflight output directory"))?;
    let temporary = parent.join(format!(
        ".dagger-rust-sdk-preflight-{}.tmp",
        std::process::id()
    ));
    let mut file = fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(&temporary)
        .map_err(|_| BinaryError::Operational("could not create sign-off output"))?;
    file.write_all(bytes)
        .and_then(|()| file.sync_all())
        .map_err(|_| BinaryError::Operational("could not persist sign-off output"))?;
    fs::rename(&temporary, path)
        .map_err(|_| BinaryError::Operational("could not publish sign-off output"))?;
    Ok(())
}

struct ProcessHostProbe {
    budgets: BTreeMap<dagger_sdk_completeness::HostPreflightPhase, NonZeroMillis>,
    workspace: PathBuf,
    retained: Vec<Vec<u8>>,
    smoke_started: bool,
    cache_image_created: bool,
    cleaned: bool,
    smoke_tool: dagger_sdk_completeness::ProvenanceId,
    smoke_engine: dagger_sdk_completeness::ProvenanceId,
}

impl ProcessHostProbe {
    fn new(plan: &HostPreflightPlan) -> Result<Self, BinaryError> {
        let workspace = std::env::current_dir()
            .map_err(|_| BinaryError::Operational("could not resolve preflight workspace"))?
            .join(format!(".dagger-rust-sdk-preflight-{}", std::process::id()));
        fs::create_dir(&workspace)
            .map_err(|_| BinaryError::Operational("could not create preflight workspace"))?;
        let current_exe = std::env::current_exe()
            .map_err(|_| BinaryError::Operational("could not resolve preflight binary"))?;
        let binary_digest = Digest::sha256(
            fs::read(current_exe)
                .map_err(|_| BinaryError::Operational("could not read preflight binary"))?,
        );
        let expected_binary_digest = plan
            .profile
            .preflight_tool
            .as_str()
            .rsplit('/')
            .next()
            .and_then(|hex| Digest::new(format!("sha256:{hex}")).ok())
            .ok_or(BinaryError::Operational(
                "preflight binary provenance is invalid",
            ))?;
        if binary_digest != expected_binary_digest {
            return Err(BinaryError::Operational(
                "preflight binary provenance does not match profile",
            ));
        }
        Ok(Self {
            budgets: plan.profile.phase_budgets.clone(),
            workspace,
            retained: Vec::new(),
            smoke_started: false,
            cache_image_created: false,
            cleaned: false,
            smoke_tool: plan.profile.smoke_tool.clone(),
            smoke_engine: plan.profile.smoke_engine.clone(),
        })
    }

    fn budget(&self, step: HostPreflightStep) -> Duration {
        Duration::from_millis(
            self.budgets
                .get(&step.phase())
                .expect("validated plan has every phase budget")
                .get(),
        )
    }

    fn command<I, S>(
        &mut self,
        step: HostPreflightStep,
        program: &str,
        args: I,
    ) -> Result<ProcessOutput, HostProbeError>
    where
        I: IntoIterator<Item = S>,
        S: AsRef<OsStr>,
    {
        let output = run_bounded(program, args, self.budget(step))
            .map_err(|kind| HostProbeError { step, kind })?;
        self.retained.push(output.stdout.clone());
        self.retained.push(output.stderr.clone());
        if output.status != 0 {
            return Err(HostProbeError {
                step,
                kind: HostProbeErrorKind::Unavailable,
            });
        }
        Ok(output)
    }

    fn cleanup(&mut self) -> Result<(), HostProbeErrorKind> {
        if self.cleaned {
            return Ok(());
        }
        let mut failed = false;
        if self.smoke_started {
            failed |= !run_bounded(
                "docker",
                ["rm", "--force", SMOKE_CONTAINER],
                Duration::from_secs(60),
            )
            .is_ok_and(|output| output.status == 0);
            self.smoke_started = false;
        }
        if self.cache_image_created {
            failed |= !run_bounded(
                "docker",
                ["image", "rm", "--force", CACHE_IMAGE],
                Duration::from_secs(60),
            )
            .is_ok_and(|output| output.status == 0);
            self.cache_image_created = false;
        }
        failed |= fs::remove_dir_all(&self.workspace).is_err();
        self.cleaned = true;
        if failed {
            Err(HostProbeErrorKind::CleanupFailed)
        } else {
            Ok(())
        }
    }

    fn observe_host(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let os = text(step, self.command(step, "uname", ["-s"])?.stdout)?;
        let arch = text(step, self.command(step, "uname", ["-m"])?.stdout)?;
        let cpu_count = std::thread::available_parallelism()
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?
            .get();
        let memory = fs::read_to_string("/proc/meminfo")
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let memory_kib = memory
            .lines()
            .find_map(|line| {
                line.strip_prefix("MemTotal:")?
                    .split_whitespace()
                    .next()?
                    .parse::<u64>()
                    .ok()
            })
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let workspace = text(
            step,
            self.command(step, "df", ["--output=avail", "-B1", "."])?
                .stdout,
        )?;
        let workspace_bytes = workspace
            .lines()
            .last()
            .and_then(|line| line.trim().parse::<u64>().ok())
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let platform = match (os.as_str(), arch.as_str()) {
            ("Linux", "x86_64") => PlatformDescriptor::linux_amd64(),
            _ => return Err(probe_error(step, HostProbeErrorKind::InvalidOutput)),
        };
        Ok(HostStepResult::HostResources {
            observation: HostResourceObservation {
                platform,
                cpu_count: NonZeroCount::new(
                    u32::try_from(cpu_count)
                        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                )
                .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                memory_bytes: NonZeroBytes::new(memory_kib.saturating_mul(1024))
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                workspace_bytes: NonZeroBytes::new(workspace_bytes)
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
            },
        })
    }

    fn observe_daemon(
        &mut self,
        step: HostPreflightStep,
    ) -> Result<HostStepResult, HostProbeError> {
        let version = text(
            step,
            self.command(
                step,
                "docker",
                [
                    "version",
                    "--format",
                    "{{.Client.Version}}|{{.Server.Version}}|{{.Server.APIVersion}}",
                ],
            )?
            .stdout,
        )?;
        let fields = version.split('|').collect::<Vec<_>>();
        if fields.len() != 3 || fields[0] != "29.3.0" {
            return Err(probe_error(step, HostProbeErrorKind::InvalidOutput));
        }
        let driver = text(
            step,
            self.command(step, "docker", ["info", "--format", "{{.Driver}}"])?
                .stdout,
        )?;
        let docker_path = find_in_path("docker")
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let docker_digest = Digest::sha256(
            fs::read(docker_path)
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        if docker_digest.as_str()
            != "sha256:b803740c076b46942159eab6ab7a5678ec6e4e3beec330487f5984fa02c06e10"
        {
            return Err(probe_error(step, HostProbeErrorKind::InvalidOutput));
        }
        let workspace = text(
            step,
            self.command(step, "df", ["--output=avail", "-B1", "."])?
                .stdout,
        )?;
        let storage_bytes = workspace
            .lines()
            .last()
            .and_then(|line| line.trim().parse::<u64>().ok())
            .ok_or_else(|| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        Ok(HostStepResult::ContainerDaemon {
            observation: ContainerDaemonObservation {
                available: true,
                api_version: NonEmptyText::new(fields[2])
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                storage_driver: NonEmptyText::new(driver.clone())
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                storage_bytes: NonZeroBytes::new(storage_bytes)
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?,
                privileged_containers: true,
                // A record must become stale if daemon behaviour or the exact client adapter
                // changes, even when the advertised API version remains stable.
                daemon_identity: Digest::sha256(format!(
                    "{version}|{driver}|{}",
                    docker_digest.as_str()
                )),
            },
        })
    }

    fn persistent_canary(
        &mut self,
        step: HostPreflightStep,
    ) -> Result<HostStepResult, HostProbeError> {
        let path = self.workspace.join("persistent-canary");
        fs::write(&path, CANARY).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let before = Digest::sha256(
            fs::read(&path).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        // A new fixed process observes the bytes; no process state is reused to establish
        // persistence.
        let path_arg = path.as_os_str().to_owned();
        self.command(step, "test", [OsStr::new("-f"), path_arg.as_os_str()])?;
        let after = Digest::sha256(
            fs::read(&path).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        Ok(HostStepResult::PersistentCanary {
            before,
            after_restart: after,
            restart_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn export_payload(
        &mut self,
        step: HostPreflightStep,
    ) -> Result<HostStepResult, HostProbeError> {
        let archive = self.workspace.join("canary.tar");
        let imported = self.workspace.join("imported");
        fs::create_dir(&imported)
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let archive_arg = archive.as_os_str().to_owned();
        let workspace_arg = self.workspace.as_os_str().to_owned();
        self.command(
            step,
            "tar",
            [
                OsStr::new("-C"),
                workspace_arg.as_os_str(),
                OsStr::new("-cf"),
                archive_arg.as_os_str(),
                OsStr::new("persistent-canary"),
            ],
        )?;
        let imported_arg = imported.as_os_str().to_owned();
        self.command(
            step,
            "tar",
            [
                OsStr::new("-C"),
                imported_arg.as_os_str(),
                OsStr::new("-xf"),
                archive_arg.as_os_str(),
            ],
        )?;
        let exported = Digest::sha256(CANARY);
        let imported = Digest::sha256(
            fs::read(imported.join("persistent-canary"))
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?,
        );
        Ok(HostStepResult::ExportedPayload { exported, imported })
    }

    fn cache_reuse(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let context = self.workspace.join("cache-context");
        fs::create_dir(&context).map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        fs::write(context.join("canary"), CANARY)
            .and_then(|()| {
                fs::write(
                    context.join("Dockerfile"),
                    "FROM scratch\nCOPY canary /canary\n",
                )
            })
            .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?;
        let first_iid = self.workspace.join("first.iid");
        let second_iid = self.workspace.join("second.iid");
        let first_args = build_args(&context, &first_iid);
        self.command(step, "docker", &first_args)?;
        self.cache_image_created = true;
        let second_args = build_args(&context, &second_iid);
        let second_output = self.command(step, "docker", &second_args)?;
        let first_output = Digest::new(
            fs::read_to_string(first_iid)
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?
                .trim(),
        )
        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let second_output_digest = Digest::new(
            fs::read_to_string(second_iid)
                .map_err(|_| probe_error(step, HostProbeErrorKind::Unavailable))?
                .trim(),
        )
        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        let output_text = String::from_utf8_lossy(&second_output.stderr).to_string()
            + &String::from_utf8_lossy(&second_output.stdout);
        Ok(HostStepResult::CacheReuse {
            first_output,
            second_output: second_output_digest,
            // Structured progress proves reuse directly; matching content-addressed image IDs
            // alone cannot distinguish cache reuse from an identical rebuild.
            reused: output_text.contains("\"cached\":true"),
        })
    }

    fn start_smoke(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let _ = run_bounded(
            "docker",
            ["rm", "--force", SMOKE_CONTAINER],
            Duration::from_secs(30),
        );
        self.command(
            step,
            "docker",
            [
                "run",
                "--detach",
                "--privileged",
                "--name",
                SMOKE_CONTAINER,
                "--pull",
                "never",
                "--publish",
                "127.0.0.1::1234",
                SMOKE_IMAGE,
                "--addr",
                "tcp://0.0.0.0:1234",
            ],
        )?;
        self.smoke_started = true;
        Ok(HostStepResult::SmokeStarted {
            smoke_tool: self.smoke_tool.clone(),
            smoke_engine: self.smoke_engine.clone(),
            start_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn probe_smoke(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let deadline = Instant::now() + self.budget(step);
        let mut reachable = false;
        while Instant::now() < deadline {
            let output = self.command(step, "docker", ["port", SMOKE_CONTAINER, "1234/tcp"])?;
            if let Ok(address) = text(step, output.stdout).and_then(|value| {
                value
                    .parse::<SocketAddr>()
                    .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))
            }) && TcpStream::connect_timeout(&address, Duration::from_secs(1)).is_ok()
            {
                reachable = true;
                break;
            }
            thread::sleep(Duration::from_millis(250));
        }
        Ok(HostStepResult::SmokeServiceProbed {
            reachable,
            probe_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn stop_smoke(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        self.command(step, "docker", ["rm", "--force", SMOKE_CONTAINER])?;
        self.smoke_started = false;
        let absent = run_bounded(
            "docker",
            ["inspect", SMOKE_CONTAINER],
            Duration::from_secs(10),
        )
        .is_ok_and(|output| output.status != 0);
        Ok(HostStepResult::SmokeStopped {
            stopped: true,
            reaped: absent,
            stop_count: NonZeroCount::new(1).expect("one is non-zero"),
        })
    }

    fn scan_output(&mut self, step: HostPreflightStep) -> Result<HostStepResult, HostProbeError> {
        let (inspected_bytes, canary_matches) =
            scan_retained_output(self.retained.iter().map(Vec::as_slice), &[CANARY])
                .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))?;
        Ok(HostStepResult::RetainedOutputScanned {
            inspected_bytes,
            canary_matches,
        })
    }
}

impl HostProbe for ProcessHostProbe {
    fn observe(&mut self, step: &HostPreflightStep) -> Result<HostStepObservation, HostProbeError> {
        let started = Instant::now();
        let result = match *step {
            HostPreflightStep::ObserveHost => self.observe_host(*step),
            HostPreflightStep::ObserveContainerDaemon => self.observe_daemon(*step),
            HostPreflightStep::RoundTripPersistentCanary => self.persistent_canary(*step),
            HostPreflightStep::RoundTripExportedPayload => self.export_payload(*step),
            HostPreflightStep::ObserveCacheReuse => self.cache_reuse(*step),
            HostPreflightStep::StartSmokeEngine => self.start_smoke(*step),
            HostPreflightStep::ProbeSmokeService => self.probe_smoke(*step),
            HostPreflightStep::StopSmokeEngine => self.stop_smoke(*step),
            HostPreflightStep::ScanRetainedOutput => self.scan_output(*step),
        }?;
        let elapsed = NonZeroMillis::new(
            u64::try_from(started.elapsed().as_millis().max(1))
                .map_err(|_| probe_error(*step, HostProbeErrorKind::TimedOut))?,
        )
        .map_err(|_| probe_error(*step, HostProbeErrorKind::TimedOut))?;
        Ok(HostStepObservation {
            step: *step,
            elapsed,
            result,
        })
    }
}

impl Drop for ProcessHostProbe {
    fn drop(&mut self) {
        let _ = self.cleanup();
    }
}

struct ProcessOutput {
    status: i32,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run_bounded<I, S>(
    program: &str,
    args: I,
    timeout: Duration,
) -> Result<ProcessOutput, HostProbeErrorKind>
where
    I: IntoIterator<Item = S>,
    S: AsRef<OsStr>,
{
    let mut child = Command::new(program)
        .args(args)
        .env(
            "DAGGER_PREFLIGHT_CANARY",
            std::str::from_utf8(CANARY).expect("canary is UTF-8"),
        )
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|_| HostProbeErrorKind::Unavailable)?;
    let stdout = child.stdout.take().ok_or(HostProbeErrorKind::Unavailable)?;
    let stderr = child.stderr.take().ok_or(HostProbeErrorKind::Unavailable)?;
    let stdout_reader = thread::spawn(move || read_bounded(stdout));
    let stderr_reader = thread::spawn(move || read_bounded(stderr));
    let deadline = Instant::now() + timeout;
    let status = loop {
        if let Some(status) = child
            .try_wait()
            .map_err(|_| HostProbeErrorKind::Unavailable)?
        {
            break status;
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            return Err(HostProbeErrorKind::TimedOut);
        }
        thread::sleep(Duration::from_millis(25));
    };
    let stdout = stdout_reader
        .join()
        .map_err(|_| HostProbeErrorKind::InvalidOutput)??;
    let stderr = stderr_reader
        .join()
        .map_err(|_| HostProbeErrorKind::InvalidOutput)??;
    Ok(ProcessOutput {
        status: status.code().unwrap_or(-1),
        stdout,
        stderr,
    })
}

fn read_bounded(mut reader: impl Read) -> Result<Vec<u8>, HostProbeErrorKind> {
    let mut retained = Vec::new();
    let mut buffer = [0_u8; 8 * 1024];
    let mut total = 0_usize;
    loop {
        let read = reader
            .read(&mut buffer)
            .map_err(|_| HostProbeErrorKind::InvalidOutput)?;
        if read == 0 {
            break;
        }
        total = total.saturating_add(read);
        if retained.len() < MAX_PROCESS_OUTPUT {
            let remaining = MAX_PROCESS_OUTPUT - retained.len();
            retained.extend_from_slice(&buffer[..read.min(remaining)]);
        }
    }
    if total > MAX_PROCESS_OUTPUT {
        Err(HostProbeErrorKind::InvalidOutput)
    } else {
        Ok(retained)
    }
}

fn text(step: HostPreflightStep, bytes: Vec<u8>) -> Result<String, HostProbeError> {
    String::from_utf8(bytes)
        .map(|value| value.trim().to_owned())
        .map_err(|_| probe_error(step, HostProbeErrorKind::InvalidOutput))
}

fn probe_error(step: HostPreflightStep, kind: HostProbeErrorKind) -> HostProbeError {
    HostProbeError { step, kind }
}

fn cleanup_diagnostic() -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::SignoffHostPreflightFailed,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Cleanup),
            ..DiagnosticCoordinate::default()
        },
        "preflight cleanup failed",
    )
}

fn find_in_path(program: &str) -> Option<PathBuf> {
    std::env::var_os("PATH")?
        .to_string_lossy()
        .split(':')
        .map(Path::new)
        .map(|directory| directory.join(program))
        .find(|candidate| candidate.is_file())
}

fn build_args<'a>(context: &'a Path, iid: &'a Path) -> Vec<&'a OsStr> {
    vec![
        OsStr::new("build"),
        OsStr::new("--provenance=false"),
        OsStr::new("--progress=rawjson"),
        OsStr::new("--tag"),
        OsStr::new(CACHE_IMAGE),
        OsStr::new("--iidfile"),
        iid.as_os_str(),
        context.as_os_str(),
    ]
}
