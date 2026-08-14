//! Admission of raw orchestration observations into Rust-owned sign-off evidence.
//!
//! The Dagger adapter owns graph construction and observes process results. It does not construct
//! canonical engine, baseline, case-binding, capability, or verdict identities. This module is the
//! narrow translation boundary: it checks the adapter's complete row set and derives every policy
//! identity from the already admitted Rust plan, catalog, artifact, platform, and security inputs.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_bytes, canonical_digest};
use crate::model::{
    CanonicalSet, CommitSha, DaggerVersion, Digest, RepositoryRelativePath, TargetDigest,
};

use super::{
    AdmittedArtifact, AdmittedStableConnector, ArtifactComponent, ArtifactCounters,
    ArtifactImportReceipt, ArtifactScannerObservation, ArtifactSecurityReport,
    AttemptSharedWorkCounters, CaseAttempt, CaseAttemptOutcome, CaseCatalog, CaseExecutionBinding,
    CaseProgram, ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DependencyDescriptorObservation, DiagnosticCoordinate,
    DiagnosticPhase, ExactEngineIdentity, ExecutionInfrastructureFailureClass,
    ForbiddenSignoffEvent, InstalledBaselineObservation, InstalledRustBaseline, NonZeroCount,
    NonZeroMillis, ObservableFixtureProgramRegistry, RustScenarioRealization, RustScenarioRegistry,
    RustSdkDependencyDescriptor, SignoffCaseId, SignoffCaseObservation, SignoffExecutionCounts,
    SignoffObservation, SignoffPhaseTimings, SignoffRunPlan, StableConnectorObservation,
    StandaloneExample, admit_artifact_import_receipt, admit_installed_rust_baseline,
    admit_stable_connector, bind_case_execution, compile_fixed_case_program_registry,
    derive_case_namespaces, exact_engine_identity_digest, signoff_run_plan_digest,
    validate_case_observation,
};

/// Closed attempt result vocabulary emitted by the orchestration adapter.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FacadeAttemptOutcome {
    /// The reviewed observable assertion passed.
    Passed,
    /// The assertion failed and may not be retried.
    AssertionFailed,
    /// The orchestration transport failed before an assertion completed.
    OrchestrationTransport,
    /// An immutable remote input could not be fetched.
    ImmutableRemoteFetch,
    /// The isolated runner could not provide capacity.
    RunnerCapacity,
}

/// Closed local output encoding retained from one standalone example.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FacadeStandaloneOutputFormat {
    /// Raw executable file produced by the CLI example.
    Executable,
    /// OCI image archive whose layers were forced to Gzip compression.
    OciGzip,
}

/// Structured, credential-free evidence from one standalone example build.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FacadeStandaloneExampleEvidence {
    /// Exact reviewed fixture identity which binds source, lockfile, and dependency policy.
    pub fixture_digest: Digest,
    /// Runtime-resolved identity for every declared public image reference.
    pub resolved_images: BTreeMap<String, Digest>,
    /// Fixed workspace-relative output path.
    pub output_path: RepositoryRelativePath,
    /// SHA-256 identity of the complete retained output bytes.
    pub output_digest: Digest,
    /// Exact retained output byte count.
    pub output_size_bytes: u64,
    /// Closed output encoding expected by post-build inspection.
    pub output_format: FacadeStandaloneOutputFormat,
    /// Credential uses observed while resolving public dependencies.
    pub credential_uses: u32,
    /// Registry pushes, uploads, or other publication attempts.
    pub publication_attempts: u32,
}

/// One raw catalog-row observation returned by the Dagger adapter.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FacadeCaseObservation {
    /// Stable catalog case identity.
    pub case_id: SignoffCaseId,
    /// Stable typed program key retained for graph audit.
    pub program: String,
    /// Reviewed production boundary retained for graph audit.
    pub boundary: String,
    /// Concrete executor selector retained for graph audit.
    pub execution_selector: String,
    /// True for exactly one representative of each physical execution group.
    pub executed: bool,
    /// Ordered terminal attempt results.
    pub attempt_outcomes: Vec<FacadeAttemptOutcome>,
    /// Successful observable-result identity; absent for a failed final attempt.
    pub observation_digest: Option<Digest>,
    /// Structured standalone-build evidence; absent for every other program.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub standalone_example_evidence: Option<FacadeStandaloneExampleEvidence>,
    /// Sum of retained attempt durations.
    pub elapsed_millis: u64,
}

/// Complete bounded raw observation returned by the one top-level sign-off graph.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RawSignoffFacadeObservation {
    /// Durable adapter format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target observed by the adapter.
    pub target_digest: TargetDigest,
    /// Evaluated fork revision.
    pub subject_revision: CommitSha,
    /// Closed case catalog identity.
    pub case_catalog_digest: Digest,
    /// Consume-only implementation closure identity.
    pub closure_bundle_digest: Digest,
    /// Supported Linux/macOS native-platform evidence identity.
    pub platform_matrix_digest: Digest,
    /// Exact artifact manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Exact retained OCI payload identity.
    pub artifact_payload_digest: Digest,
    /// Raw scanner-result identity retained for security-report correlation.
    pub scanner_result_digest: Digest,
    /// Canonical Rust-translated scanner observation produced inside the sign-off graph.
    pub scanner_observation: ArtifactScannerObservation,
    /// Canonical Rust-admitted live canary and retained-evidence result.
    pub secret_report: super::SecretEvidenceReport,
    /// Typed stable-default-connector evidence produced by the production Rust connector.
    pub stable_connector: StableConnectorObservation,
    /// Independent adapter observation of the validated target engine fields.
    pub engine_observation_digest: Digest,
    /// Independent adapter observation of the installed workspace contents.
    pub baseline_observation_digest: Digest,
    /// Dagger-native identity of the complete installed workspace directory.
    pub baseline_directory_digest: Digest,
    /// Installed workspace configuration identity.
    pub installed_config_digest: Digest,
    /// Whether the installed workspace began as a clean initialized Git repository.
    pub clean_git_workspace: bool,
    /// Whether only the exact artifact CLI was discoverable through `PATH`.
    pub artifact_cli_only_on_path: bool,
    /// Whether an ambient host Dagger CLI remained visible.
    pub host_cli_visible: bool,
    /// Whether pre-existing Dagger configuration survived into the installed baseline.
    pub stale_installed_config: bool,
    /// Engine-service starts observed before the exact artifact Import identity was validated.
    pub service_starts_before_validation: u32,
    /// Packaged dependency coordinate decoded from the installed workspace.
    pub dependency: DependencyDescriptorObservation,
    /// Component identities independently observed from the imported target container.
    pub verified_component_digests: BTreeMap<ArtifactComponent, Digest>,
    /// Rust descriptor identity independently observed from the imported target container.
    pub verified_rust_descriptor_digest: Digest,
    /// Packaged dependency coordinate independently observed from the imported target container.
    pub verified_rust_dependency: RustSdkDependencyDescriptor,
    /// Direct descriptor-byte identity independently observed from the imported target container.
    pub verified_rust_dependency_descriptor_digest: Digest,
    /// Canonical receipt emitted by the sole exact artifact Import graph.
    pub artifact_import_receipt: ArtifactImportReceipt,
    /// Independently retained identity of the canonical Import receipt.
    pub artifact_import_receipt_digest: Digest,
    /// Digest-pinned runner image identity.
    pub runner_image_digest: Digest,
    /// Artifact constructions observed by the graph.
    pub artifact_constructions: u32,
    /// Artifact imports observed by the graph.
    pub artifact_imports: u32,
    /// Exact-target engine component builds.
    pub engine_component_builds: u32,
    /// Exact-target CLI component builds.
    pub cli_component_builds: u32,
    /// Mandatory Go runtime component builds.
    pub go_runtime_component_builds: u32,
    /// Packaged Rust SDK component builds.
    pub rust_sdk_component_builds: u32,
    /// Orchestration engine starts observed by the invocation.
    pub orchestration_engine_starts: u32,
    /// Exact-target engine starts.
    pub exact_target_engine_starts: u32,
    /// Exact-target engine stops.
    pub exact_target_engine_stops: u32,
    /// Exact-target child reaps.
    pub exact_target_child_reaps: u32,
    /// Installed Rust baseline materializations.
    pub rust_baseline_materializations: u32,
    /// Physical reviewed Rust invocations before catalog-row expansion.
    pub case_executions: u32,
    /// Child implementation evidence replays.
    pub closure_replays: u32,
    /// Unrelated graph actions.
    pub unrelated_actions: u32,
    /// Registry pushes, release uploads, or other external publication attempts.
    pub external_publication_attempts: u32,
    /// Complete catalog-row observation set.
    pub cases: Vec<FacadeCaseObservation>,
    /// Artifact build or import duration.
    pub artifact_millis: u64,
    /// Exact payload security-scan duration.
    pub security_scan_millis: u64,
    /// Exact-target engine startup duration.
    pub engine_startup_millis: u64,
    /// Sole Rust baseline installation duration.
    pub rust_installation_millis: u64,
    /// Sum of expanded catalog-row attempt durations.
    pub case_execution_millis: u64,
    /// Physical grouped execution duration retained for audit.
    pub runnable_execution_millis: u64,
    /// Engine stop, reap, and cleanup duration.
    pub cleanup_millis: u64,
    /// Exact sum of the six verdict phase durations.
    pub total_millis: u64,
}

/// Rust-owned route expected from the orchestration adapter for one catalog row.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FacadeRouteExpectation {
    program: String,
    boundary: String,
    execution_selector: String,
    executed: bool,
}

impl FacadeRouteExpectation {
    /// Borrows the stable typed program key.
    pub fn program(&self) -> &str {
        &self.program
    }

    /// Borrows the reviewed production-boundary spelling.
    pub fn boundary(&self) -> &str {
        &self.boundary
    }

    /// Borrows the concrete Rust executor selector.
    pub fn execution_selector(&self) -> &str {
        &self.execution_selector
    }

    /// Returns whether this row must represent its physical execution group.
    pub const fn executed(&self) -> bool {
        self.executed
    }
}

/// Complete Rust-owned case-route projection used to audit raw Go observations.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FacadeRouteRegistry {
    routes: BTreeMap<SignoffCaseId, FacadeRouteExpectation>,
    physical_executions: u32,
}

impl FacadeRouteRegistry {
    /// Borrows every expected route in canonical case order.
    pub fn routes(&self) -> &BTreeMap<SignoffCaseId, FacadeRouteExpectation> {
        &self.routes
    }

    /// Returns the exact number of physical Rust executions represented by the routes.
    pub const fn physical_executions(&self) -> u32 {
        self.physical_executions
    }
}

/// Compiles the exact adapter route projection from fully admitted Rust-owned registries.
pub fn compile_facade_route_registry(
    catalog: &CaseCatalog,
    observable: &ObservableFixtureProgramRegistry,
    scenarios: &RustScenarioRegistry,
) -> Result<FacadeRouteRegistry, ConformanceDiagnosticSet> {
    let fixed = compile_fixed_case_program_registry()?;
    let mut routes = BTreeMap::new();
    let mut execution_groups = BTreeSet::new();
    for (case_id, case) in catalog.cases() {
        let (program, boundary, selector, invocation, route_policy_digest) = match &case.program {
            CaseProgram::IntegrationAssertion { fixture } => {
                let program = observable.programs().get(fixture).ok_or_else(|| {
                    facade_error(
                        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                        "integration case has no admitted observable route",
                        Some(case_id.clone()),
                    )
                })?;
                let registration = scenarios.registrations().get(case_id).ok_or_else(|| {
                    facade_error(
                        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                        "integration case has no admitted Rust realization",
                        Some(case_id.clone()),
                    )
                })?;
                let (selector, exact_fixture) = match &registration.realization {
                    RustScenarioRealization::GeneratedCore { realization_id, .. } => {
                        (realization_id.as_str(), true)
                    }
                    RustScenarioRealization::ReviewedRustFixture {
                        realization_id,
                        fixture_id,
                    } => (realization_id.as_str(), fixture_id == fixture),
                    RustScenarioRealization::RealizationRequired => ("", false),
                };
                if program.case_id != *case_id || program.fixture_id != *fixture || !exact_fixture {
                    return Err(facade_error(
                        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                        "integration route disagrees with its admitted Rust realization",
                        Some(case_id.clone()),
                    ));
                }
                let program_key = format!("integration-assertion/{}", fixture.as_str());
                let boundary = serde_string(&program.boundary)?;
                let selector = selector.to_owned();
                let invocation = format!("rust-scenario-conformance/{selector}");
                let route_policy_digest = canonical_digest(
                    DigestDomain::ConformanceProgramRegistry,
                    &(
                        "integration-facade-route-policy",
                        program.boundary,
                        &registration.proof_id,
                    ),
                )
                .map_err(|_| {
                    facade_error(
                        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                        "integration route policy cannot be identified",
                        Some(case_id.clone()),
                    )
                })?;
                (
                    program_key,
                    boundary,
                    selector,
                    invocation,
                    route_policy_digest,
                )
            }
            program => {
                let spec = fixed.programs().get(program).ok_or_else(|| {
                    facade_error(
                        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                        "fixed case has no admitted production route",
                        Some(case_id.clone()),
                    )
                })?;
                let program_key = facade_program_key(program)?;
                let boundary = serde_string(&spec.boundary)?;
                let (selector, grouped) = fixed_execution_selector(program)?;
                let group = if grouped {
                    format!("rust-scenario-conformance/{selector}")
                } else {
                    program_key.clone()
                };
                let route_policy_digest = canonical_digest(
                    DigestDomain::ConformanceProgramRegistry,
                    &(
                        "fixed-facade-route-policy",
                        spec.boundary,
                        spec.workspace,
                        spec.authority,
                        spec.sdk_source,
                    ),
                )
                .map_err(|_| {
                    facade_error(
                        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                        "fixed route policy cannot be identified",
                        Some(case_id.clone()),
                    )
                })?;
                (program_key, boundary, selector, group, route_policy_digest)
            }
        };
        // Sharing a selector is insufficient: aliases may fan into one invocation only when the
        // production boundary, expected predicate, timeout, retry, network, and isolation policy
        // are all identical. Otherwise a stricter row could silently inherit weaker scheduling.
        let execution_group = canonical_digest(
            DigestDomain::ConformanceProgramRegistry,
            &(
                "facade-execution-group",
                invocation,
                route_policy_digest,
                case.timeout,
                &case.retry,
                &case.network,
                case.concurrency_class,
            ),
        )
        .map_err(|_| {
            facade_error(
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "facade execution policy cannot be identified",
                Some(case_id.clone()),
            )
        })?;
        let executed = execution_groups.insert(execution_group);
        routes.insert(
            case_id.clone(),
            FacadeRouteExpectation {
                program,
                boundary,
                execution_selector: selector,
                executed,
            },
        );
    }
    let physical_executions = u32::try_from(execution_groups.len()).map_err(|_| {
        facade_error(
            ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
            "physical execution count exceeds the bounded format",
            None,
        )
    })?;
    Ok(FacadeRouteRegistry {
        routes,
        physical_executions,
    })
}

fn facade_program_key(program: &CaseProgram) -> Result<String, ConformanceDiagnosticSet> {
    let key = match program {
        CaseProgram::CommonHarness { check } => {
            format!("common-harness/{}", serde_string(check)?)
        }
        CaseProgram::StableConnector => "stable-connector/".to_owned(),
        CaseProgram::CoreShape { shape } => format!("core-shape/{}", serde_string(shape)?),
        CaseProgram::EngineIntegration { case } => {
            format!("engine-integration/{}", serde_string(case)?)
        }
        CaseProgram::ModuleAuthoring { case } => {
            format!("module-authoring/{}", serde_string(case)?)
        }
        CaseProgram::StandaloneClient { case } => {
            format!("standalone-client/{}", serde_string(case)?)
        }
        CaseProgram::StandaloneExample { example } => {
            format!("standalone-example/{}", serde_string(example)?)
        }
        CaseProgram::DefinitiveGoClient { behaviour } => {
            format!("definitive-go-client/{}", serde_string(behaviour)?)
        }
        CaseProgram::IntegrationAssertion { fixture } => {
            format!("integration-assertion/{}", fixture.as_str())
        }
    };
    Ok(key)
}

fn fixed_execution_selector(
    program: &CaseProgram,
) -> Result<(String, bool), ConformanceDiagnosticSet> {
    let selected = match program {
        CaseProgram::CommonHarness { .. } => ("realization/common-harness".to_owned(), true),
        CaseProgram::StableConnector => ("realization/stable-connector".to_owned(), true),
        CaseProgram::CoreShape { shape } => (serde_string(shape)?, false),
        CaseProgram::EngineIntegration { case } => (serde_string(case)?, false),
        CaseProgram::ModuleAuthoring { case } => {
            (format!("realization/module-{}", serde_string(case)?), true)
        }
        CaseProgram::StandaloneClient { .. } => ("realization/standalone-clients".to_owned(), true),
        CaseProgram::StandaloneExample { example } => (
            format!("standalone-example/{}", serde_string(example)?),
            false,
        ),
        CaseProgram::DefinitiveGoClient { behaviour } => (serde_string(behaviour)?, false),
        CaseProgram::IntegrationAssertion { .. } => {
            return Err(facade_error(
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "integration routes require an admitted scenario realization",
                None,
            ));
        }
    };
    Ok(selected)
}

fn serde_string<T: Serialize>(value: &T) -> Result<String, ConformanceDiagnosticSet> {
    serde_json::to_value(value)
        .ok()
        .and_then(|value| value.as_str().map(str::to_owned))
        .ok_or_else(|| {
            facade_error(
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "typed route has no stable serialized spelling",
                None,
            )
        })
}

/// Rust-owned values derived from one complete raw adapter observation.
pub struct AdmittedSignoffFacade {
    baseline: InstalledRustBaseline,
    stable_connector: AdmittedStableConnector,
    bindings: BTreeMap<SignoffCaseId, CaseExecutionBinding>,
    observation: SignoffObservation,
}

impl AdmittedSignoffFacade {
    /// Borrows the canonical installed Rust baseline.
    pub fn baseline(&self) -> &InstalledRustBaseline {
        &self.baseline
    }

    /// Borrows the admitted stable-default-connector result.
    pub fn stable_connector(&self) -> &AdmittedStableConnector {
        &self.stable_connector
    }

    /// Borrows every canonical catalog-case execution binding.
    pub fn bindings(&self) -> &BTreeMap<SignoffCaseId, CaseExecutionBinding> {
        &self.bindings
    }

    /// Borrows the complete Rust-owned sign-off observation.
    pub fn observation(&self) -> &SignoffObservation {
        &self.observation
    }

    /// Consumes the admission and returns its complete Rust-owned observation.
    pub fn into_observation(self) -> SignoffObservation {
        self.observation
    }
}

/// Translates one complete adapter result without allowing Go to manufacture policy identities.
pub fn admit_signoff_facade_observation(
    plan: &SignoffRunPlan,
    catalog: &CaseCatalog,
    route_registry: &FacadeRouteRegistry,
    artifact: &AdmittedArtifact,
    platform_matrix_digest: &Digest,
    security_report: &ArtifactSecurityReport,
    raw: RawSignoffFacadeObservation,
) -> Result<AdmittedSignoffFacade, ConformanceDiagnosticSet> {
    validate_facade_envelope(
        plan,
        catalog,
        route_registry,
        artifact,
        platform_matrix_digest,
        security_report,
        &raw,
    )?;
    let admitted_import = admit_artifact_import_receipt(
        &plan.artifact_plan,
        artifact.bundle().clone(),
        &raw.artifact_import_receipt,
    )?;
    if admitted_import.manifest_digest() != artifact.manifest_digest()
        || admitted_import.payload_digest() != artifact.payload_digest()
    {
        return Err(facade_error(
            ConformanceDiagnosticCode::SignoffArtifactStateInvalid,
            "artifact Import receipt differs from the already admitted exact artifact",
            None,
        ));
    }

    let manifest = artifact.bundle().manifest();
    let mut engine = ExactEngineIdentity {
        target_descriptor_digest: manifest.target_descriptor_digest.clone(),
        target_revision: manifest.target_revision.clone(),
        engine_version: DaggerVersion::new("v1.0.0-beta.10")
            .expect("the reviewed exact target version is valid"),
        platform: manifest.platform.clone(),
        rust_manifest_digest: manifest.rust_manifest_digest.clone(),
        rust_descriptor_digest: manifest.rust_descriptor_digest.clone(),
        identity_digest: Digest::sha256([]),
    };
    engine.identity_digest = exact_engine_identity_digest(&engine)?;
    let engine_version = engine.engine_version.clone();
    let cli_digest = manifest
        .components
        .get(&ArtifactComponent::Cli)
        .expect("admitted artifacts contain the required CLI component")
        .content_digest
        .clone();
    let baseline = admit_installed_rust_baseline(
        artifact,
        &engine_version,
        InstalledBaselineObservation {
            engine,
            artifact_manifest_digest: artifact.manifest_digest().clone(),
            artifact_payload_digest: artifact.payload_digest().clone(),
            cli_digest,
            runner_image_digest: raw.runner_image_digest.clone(),
            installed_config_digest: raw.installed_config_digest.clone(),
            dependency: raw.dependency.clone(),
            install_count: raw.rust_baseline_materializations,
            clean_git_workspace: raw.clean_git_workspace,
            artifact_cli_only_on_path: raw.artifact_cli_only_on_path,
            host_cli_visible: raw.host_cli_visible,
            stale_installed_config: raw.stale_installed_config,
            service_starts_before_validation: raw.service_starts_before_validation,
            elapsed_millis: positive_millis(raw.rust_installation_millis)?,
        },
    )?;
    let stable_connector = admit_stable_connector(&baseline, raw.stable_connector.clone())?;

    let bindings = catalog
        .cases()
        .keys()
        .map(|case_id| {
            bind_case_execution(catalog, case_id, artifact, &baseline)
                .map(|binding| (case_id.clone(), binding))
        })
        .collect::<Result<BTreeMap<_, _>, _>>()?;
    let cases = translate_cases(catalog, route_registry, &bindings, raw.cases)?;
    let artifact_counts = ArtifactCounters {
        construction: raw.artifact_constructions,
        imports: raw.artifact_imports,
        component_builds: BTreeMap::from([
            (ArtifactComponent::Engine, raw.engine_component_builds),
            (ArtifactComponent::Cli, raw.cli_component_builds),
            (
                ArtifactComponent::GoRuntime,
                raw.go_runtime_component_builds,
            ),
            (ArtifactComponent::RustSdk, raw.rust_sdk_component_builds),
        ]),
        forbidden_work: CanonicalSet::default(),
    };
    let phase_timings = SignoffPhaseTimings {
        artifact: positive_millis(raw.artifact_millis)?,
        engine_startup: positive_millis(raw.engine_startup_millis)?,
        rust_installation: positive_millis(raw.rust_installation_millis)?,
        security_scan: positive_millis(raw.security_scan_millis)?,
        case_execution: positive_millis(raw.case_execution_millis)?,
        cleanup: positive_millis(raw.cleanup_millis)?,
        total: positive_millis(raw.total_millis)?,
    };
    let observation = SignoffObservation {
        run_plan_digest: signoff_run_plan_digest(plan, catalog)?,
        host_profile_digest: plan.host_profile_digest.clone(),
        host_preflight_digest: plan.preflight_digest.clone(),
        artifact_manifest_digest: raw.artifact_manifest_digest,
        artifact_payload_digest: raw.artifact_payload_digest,
        artifact_import_receipt: raw.artifact_import_receipt,
        closure_bundle_digest: raw.closure_bundle_digest,
        platform_matrix_digest: raw.platform_matrix_digest,
        security_report_digest: security_report.report_digest.clone(),
        engine_identity_digest: baseline.engine.identity_digest.clone(),
        baseline: baseline.clone(),
        stable_connector: stable_connector.clone(),
        execution_counts: SignoffExecutionCounts {
            preflight_smoke_engine_starts: 1,
            orchestration_engine_starts: raw.orchestration_engine_starts,
            artifact: artifact_counts,
            exact_target_engine_starts: raw.exact_target_engine_starts,
            exact_target_engine_stops: raw.exact_target_engine_stops,
            exact_target_child_reaps: raw.exact_target_child_reaps,
            rust_baseline_materializations: raw.rust_baseline_materializations,
            case_executions: raw.case_executions,
            closure_replays: raw.closure_replays,
            unrelated_actions: raw.unrelated_actions,
        },
        phase_timings,
        cases,
        claimed_capability_ids: CanonicalSet::new(catalog.capability_cases().keys().cloned()),
        platform_gate_passed: true,
        security_gate_passed: true,
        secret_canary_leaks: 0,
        forbidden_events: if raw.unrelated_actions == 0 {
            Vec::new()
        } else {
            vec![ForbiddenSignoffEvent::UnrelatedSdk]
        },
    };
    Ok(AdmittedSignoffFacade {
        baseline,
        stable_connector,
        bindings,
        observation,
    })
}

fn validate_facade_envelope(
    plan: &SignoffRunPlan,
    catalog: &CaseCatalog,
    route_registry: &FacadeRouteRegistry,
    artifact: &AdmittedArtifact,
    platform_matrix_digest: &Digest,
    security_report: &ArtifactSecurityReport,
    raw: &RawSignoffFacadeObservation,
) -> Result<(), ConformanceDiagnosticSet> {
    let row_ids = raw
        .cases
        .iter()
        .map(|case| case.case_id.clone())
        .collect::<BTreeSet<_>>();
    let expected_ids = catalog.cases().keys().cloned().collect::<BTreeSet<_>>();
    let route_ids = route_registry
        .routes()
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>();
    let executed = raw.cases.iter().filter(|case| case.executed).count() as u32;
    let phase_total = [
        raw.artifact_millis,
        raw.engine_startup_millis,
        raw.rust_installation_millis,
        raw.security_scan_millis,
        raw.case_execution_millis,
        raw.cleanup_millis,
    ]
    .into_iter()
    .try_fold(0_u64, u64::checked_add);
    let case_total = raw
        .cases
        .iter()
        .try_fold(0_u64, |total, case| total.checked_add(case.elapsed_millis));
    let verified_components = artifact
        .bundle()
        .manifest()
        .components
        .iter()
        .map(|(component, record)| (*component, record.content_digest.clone()))
        .collect::<BTreeMap<_, _>>();
    let identities_match = raw.format_version == ConformanceFormatVersion::V1
        && raw.target_digest == plan.target_digest
        && raw.subject_revision == plan.subject_revision
        && raw.case_catalog_digest == plan.case_catalog_digest
        && raw.closure_bundle_digest == plan.closure_bundle_digest
        && raw.platform_matrix_digest == *platform_matrix_digest
        && raw.artifact_manifest_digest == *artifact.manifest_digest()
        && raw.artifact_payload_digest == *artifact.payload_digest()
        && raw.scanner_result_digest == security_report.scanner_result_digest
        && raw.scanner_result_digest == raw.scanner_observation.scanner_result_digest
        && raw.scanner_observation.payload_digest == raw.artifact_payload_digest
        && raw.secret_report.report_digest == security_report.secret_report_digest
        && raw.secret_report.inspected_domains == security_report.inspected_domains
        && raw.verified_component_digests == verified_components
        && raw.verified_rust_descriptor_digest
            == artifact.bundle().manifest().rust_descriptor_digest
        && raw.verified_rust_dependency == artifact.bundle().manifest().rust_dependency
        && raw.verified_rust_dependency_descriptor_digest
            == artifact
                .bundle()
                .manifest()
                .rust_dependency_descriptor_digest
        && raw.artifact_import_receipt_digest == raw.artifact_import_receipt.receipt_digest
        && security_report.artifact_import_receipt_digest == raw.artifact_import_receipt_digest
        && raw.engine_observation_digest == expected_engine_observation(plan, artifact)?
        && raw.baseline_observation_digest == expected_baseline_observation(raw);
    let rows_match = raw.cases.len() == expected_ids.len()
        && row_ids.len() == raw.cases.len()
        && row_ids == expected_ids
        && route_ids == expected_ids
        && executed == raw.case_executions
        && raw.case_executions == plan.expected_case_executions.get()
        && raw.case_executions == route_registry.physical_executions()
        && raw.runnable_execution_millis > 0
        && raw.baseline_directory_digest != Digest::sha256([]);
    let timings_match = phase_total == Some(raw.total_millis)
        && case_total == Some(raw.case_execution_millis)
        && raw.total_millis <= plan.total_budget.get();
    let side_effects_absent = raw.external_publication_attempts == 0;
    let stable_connector_matches = stable_connector_case_matches(raw)?;
    if identities_match
        && rows_match
        && timings_match
        && side_effects_absent
        && stable_connector_matches
    {
        return Ok(());
    }
    Err(facade_error(
        ConformanceDiagnosticCode::SignoffVerdictIncomplete,
        "raw sign-off facade identities rows counts or timings are incomplete",
        None,
    ))
}

fn stable_connector_case_matches(
    raw: &RawSignoffFacadeObservation,
) -> Result<bool, ConformanceDiagnosticSet> {
    let structured = canonical_bytes(&raw.stable_connector).map_err(|_| {
        facade_error(
            ConformanceDiagnosticCode::SignoffDistributionObservationInvalid,
            "stable connector evidence cannot be encoded canonically",
            None,
        )
    })?;
    let structured_digest = Digest::sha256(structured);
    let matching = raw
        .cases
        .iter()
        .filter(|case| case.program == "stable-connector/")
        .collect::<Vec<_>>();
    Ok(matches!(matching.as_slice(), [case]
        if case.attempt_outcomes.last() == Some(&FacadeAttemptOutcome::Passed)
            && case.observation_digest.as_ref() == Some(&structured_digest)
            && raw.stable_connector.session_control_succeeded
            && raw.stable_connector.authenticated_loopback_constructed))
}

fn expected_engine_observation(
    plan: &SignoffRunPlan,
    artifact: &AdmittedArtifact,
) -> Result<Digest, ConformanceDiagnosticSet> {
    let manifest = artifact.bundle().manifest();
    let manifest_bytes = canonical_bytes(manifest).map_err(|_| {
        facade_error(
            ConformanceDiagnosticCode::SignoffArtifactManifestInvalid,
            "admitted artifact manifest cannot be encoded canonically",
            None,
        )
    })?;
    let mut input = Vec::new();
    input.extend_from_slice(plan.target_digest.0.as_str().as_bytes());
    input.push(0);
    input.extend_from_slice(manifest.target_revision.as_str().as_bytes());
    input.push(0);
    input.extend_from_slice(b"v1.0.0-beta.10");
    input.push(0);
    input.extend_from_slice(&manifest_bytes);
    Ok(Digest::sha256(input))
}

fn expected_baseline_observation(raw: &RawSignoffFacadeObservation) -> Digest {
    let mut input = Vec::new();
    input.extend_from_slice(raw.installed_config_digest.as_str().as_bytes());
    input.push(0);
    input.extend_from_slice(raw.baseline_directory_digest.as_str().as_bytes());
    input.push(0);
    input.extend_from_slice(&[
        u8::from(raw.clean_git_workspace),
        u8::from(raw.artifact_cli_only_on_path),
        u8::from(raw.host_cli_visible),
        u8::from(raw.stale_installed_config),
    ]);
    input.extend_from_slice(&raw.service_starts_before_validation.to_be_bytes());
    Digest::sha256(input)
}

fn translate_cases(
    catalog: &CaseCatalog,
    route_registry: &FacadeRouteRegistry,
    bindings: &BTreeMap<SignoffCaseId, CaseExecutionBinding>,
    raw_cases: Vec<FacadeCaseObservation>,
) -> Result<Vec<SignoffCaseObservation>, ConformanceDiagnosticSet> {
    let raw = raw_cases
        .into_iter()
        .map(|case| (case.case_id.clone(), case))
        .collect::<BTreeMap<_, _>>();
    catalog
        .cases()
        .keys()
        .map(|case_id| {
            let case = raw
                .get(case_id)
                .expect("envelope proves the complete row set");
            let definition = &catalog.cases()[case_id];
            let binding = &bindings[case_id];
            let route = &route_registry.routes[case_id];
            if case.program != route.program
                || case.boundary != route.boundary
                || case.execution_selector != route.execution_selector
                || case.executed != route.executed
            {
                return Err(facade_error(
                    ConformanceDiagnosticCode::SignoffCaseFailed,
                    "raw case route or representative differs from Rust policy",
                    Some(case_id.clone()),
                ));
            }
            let final_index = case.attempt_outcomes.len().checked_sub(1).ok_or_else(|| {
                facade_error(
                    ConformanceDiagnosticCode::SignoffCaseFailed,
                    "raw case omitted its complete attempt history",
                    Some(case_id.clone()),
                )
            })?;
            let attempt_elapsed = partition_attempt_elapsed(
                case.elapsed_millis,
                case.attempt_outcomes.len(),
                case_id,
            )?;
            let final_passed = case.attempt_outcomes[final_index] == FacadeAttemptOutcome::Passed;
            if final_passed != case.observation_digest.is_some() {
                return Err(facade_error(
                    ConformanceDiagnosticCode::SignoffCaseFailed,
                    "raw case observation identity does not match its final outcome",
                    Some(case_id.clone()),
                ));
            }
            validate_standalone_example_evidence(definition, case, final_passed)?;
            let attempts = case
                .attempt_outcomes
                .iter()
                .copied()
                .zip(attempt_elapsed)
                .enumerate()
                .map(|(index, (outcome, elapsed_millis))| {
                    let attempt = u32::try_from(index + 1)
                        .ok()
                        .and_then(|value| NonZeroCount::new(value).ok())
                        .ok_or_else(|| {
                            facade_error(
                                ConformanceDiagnosticCode::SignoffCaseFailed,
                                "raw case attempt count exceeds the bounded format",
                                Some(case_id.clone()),
                            )
                        })?;
                    // The adapter retains one successful observation identity because a retry can
                    // have at most one final pass. Earlier failures remain fully represented by
                    // their typed outcome and freshly derived isolated attempt namespace.
                    let observation_digest = (index == final_index)
                        .then(|| case.observation_digest.clone())
                        .flatten();
                    Ok(CaseAttempt {
                        attempt,
                        execution_binding_digest: binding.execution_binding_digest.clone(),
                        namespaces: derive_case_namespaces(binding, attempt)?,
                        shared_work: AttemptSharedWorkCounters::default(),
                        elapsed_millis,
                        outcome: translate_outcome(outcome, observation_digest, case_id)?,
                    })
                })
                .collect::<Result<Vec<_>, ConformanceDiagnosticSet>>()?;
            let final_outcome = attempts
                .last()
                .expect("the non-empty history was checked before translation")
                .outcome
                .clone();
            let observation = SignoffCaseObservation {
                case_id: case_id.clone(),
                execution_binding_digest: binding.execution_binding_digest.clone(),
                attempts,
                final_outcome,
                elapsed_millis: positive_millis(case.elapsed_millis)?,
            };
            // Keep retry policy in the Rust-owned validator so assertion absorption, declared
            // infrastructure classes, bounds, and identity reuse cannot drift at this facade.
            validate_case_observation(definition, binding, &observation)?;
            Ok(observation)
        })
        .collect()
}

fn validate_standalone_example_evidence(
    definition: &super::CaseDefinition,
    observation: &FacadeCaseObservation,
    final_passed: bool,
) -> Result<(), ConformanceDiagnosticSet> {
    let CaseProgram::StandaloneExample { example } = definition.program else {
        if observation.standalone_example_evidence.is_none() {
            return Ok(());
        }
        return Err(facade_error(
            ConformanceDiagnosticCode::SignoffCaseIsolationViolation,
            "non-example case retained standalone build evidence",
            Some(definition.id.clone()),
        ));
    };
    if !final_passed {
        if observation.standalone_example_evidence.is_none() {
            return Ok(());
        }
        return Err(facade_error(
            ConformanceDiagnosticCode::SignoffCaseFailed,
            "failed standalone build retained successful output evidence",
            Some(definition.id.clone()),
        ));
    }
    let evidence = observation
        .standalone_example_evidence
        .as_ref()
        .ok_or_else(|| {
            facade_error(
                ConformanceDiagnosticCode::SignoffCaseFailed,
                "passed standalone build omitted structured output evidence",
                Some(definition.id.clone()),
            )
        })?;
    let (images, output_path, output_format): (
        &'static [&'static str],
        &'static str,
        FacadeStandaloneOutputFormat,
    ) = match example {
        StandaloneExample::Cli => (
            &["rust:1.97.1-slim-bookworm"],
            "build/cli",
            FacadeStandaloneOutputFormat::Executable,
        ),
        StandaloneExample::Backend => (
            &[
                "gcr.io/distroless/static-debian12",
                "rust:1.97.1-alpine3.22",
            ],
            "build/backend-image.tar",
            FacadeStandaloneOutputFormat::OciGzip,
        ),
        StandaloneExample::Frontend => (
            &["nginx:1.24.0-alpine3.17", "rust:1.97.1"],
            "build/frontend-image.tar",
            FacadeStandaloneOutputFormat::OciGzip,
        ),
    };
    let expected_images = images.iter().copied().collect::<BTreeSet<_>>();
    let observed_images = evidence
        .resolved_images
        .keys()
        .map(String::as_str)
        .collect::<BTreeSet<_>>();
    let structured_digest = canonical_bytes(evidence).map(Digest::sha256).map_err(|_| {
        facade_error(
            ConformanceDiagnosticCode::SignoffCaseFailed,
            "standalone build evidence cannot be encoded canonically",
            Some(definition.id.clone()),
        )
    })?;
    let valid = evidence.fixture_digest == definition.fixture_digest
        && observed_images == expected_images
        && evidence
            .resolved_images
            .values()
            .all(|digest| digest != &Digest::sha256([]))
        && evidence.output_path.as_str() == output_path
        && evidence.output_digest != Digest::sha256([])
        && (1..=256 * 1024 * 1024).contains(&evidence.output_size_bytes)
        && evidence.output_format == output_format
        && evidence.credential_uses == 0
        && evidence.publication_attempts == 0
        && observation.observation_digest.as_ref() == Some(&structured_digest);
    if valid {
        return Ok(());
    }
    Err(facade_error(
        ConformanceDiagnosticCode::SignoffCaseIsolationViolation,
        "standalone build source dependency output or side-effect evidence is invalid",
        Some(definition.id.clone()),
    ))
}

fn partition_attempt_elapsed(
    total_millis: u64,
    attempt_count: usize,
    case_id: &SignoffCaseId,
) -> Result<Vec<NonZeroMillis>, ConformanceDiagnosticSet> {
    let count = u64::try_from(attempt_count).map_err(|_| {
        facade_error(
            ConformanceDiagnosticCode::SignoffCaseFailed,
            "raw case attempt count exceeds the bounded format",
            Some(case_id.clone()),
        )
    })?;
    if count == 0 || total_millis < count {
        return Err(facade_error(
            ConformanceDiagnosticCode::SignoffCaseFailed,
            "raw case duration cannot retain a positive duration for every attempt",
            Some(case_id.clone()),
        ));
    }
    let common = total_millis / count;
    let remainder = total_millis % count;
    (0..count)
        .map(|index| {
            // The raw ABI observes only aggregate case time. This canonical partition preserves
            // that measured total while satisfying the positive attempt-time invariant; only the
            // aggregate, not an individual normalized share, is an observed timing measurement.
            NonZeroMillis::new(common + u64::from(index < remainder)).map_err(|_| {
                facade_error(
                    ConformanceDiagnosticCode::SignoffCaseFailed,
                    "raw case duration cannot retain a positive duration for every attempt",
                    Some(case_id.clone()),
                )
            })
        })
        .collect()
}

fn translate_outcome(
    outcome: FacadeAttemptOutcome,
    observation_digest: Option<Digest>,
    case_id: &SignoffCaseId,
) -> Result<CaseAttemptOutcome, ConformanceDiagnosticSet> {
    match outcome {
        FacadeAttemptOutcome::Passed => observation_digest
            .map(|observation_digest| CaseAttemptOutcome::Passed { observation_digest })
            .ok_or_else(|| {
                facade_error(
                    ConformanceDiagnosticCode::SignoffCaseFailed,
                    "passed raw case omitted its observation identity",
                    Some(case_id.clone()),
                )
            }),
        FacadeAttemptOutcome::AssertionFailed => Ok(CaseAttemptOutcome::AssertionFailed {
            diagnostic: facade_case_diagnostic(case_id, "reviewed Rust assertion failed"),
        }),
        FacadeAttemptOutcome::OrchestrationTransport
        | FacadeAttemptOutcome::ImmutableRemoteFetch
        | FacadeAttemptOutcome::RunnerCapacity => {
            let class = match outcome {
                FacadeAttemptOutcome::OrchestrationTransport => {
                    ExecutionInfrastructureFailureClass::OrchestrationTransport
                }
                FacadeAttemptOutcome::ImmutableRemoteFetch => {
                    ExecutionInfrastructureFailureClass::ImmutableRemoteFetch
                }
                FacadeAttemptOutcome::RunnerCapacity => {
                    ExecutionInfrastructureFailureClass::RunnerCapacity
                }
                _ => unreachable!(),
            };
            Ok(CaseAttemptOutcome::InfrastructureFailed {
                class,
                diagnostic: facade_case_diagnostic(case_id, "reviewed infrastructure failed"),
            })
        }
    }
}

fn positive_millis(value: u64) -> Result<NonZeroMillis, ConformanceDiagnosticSet> {
    NonZeroMillis::new(value).map_err(|_| {
        facade_error(
            ConformanceDiagnosticCode::SignoffVerdictIncomplete,
            "raw sign-off duration is zero",
            None,
        )
    })
}

fn facade_case_diagnostic(case_id: &SignoffCaseId, detail: &'static str) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::SignoffCaseFailed,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Case),
            case_id: Some(case_id.clone()),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn facade_error(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
    case_id: Option<SignoffCaseId>,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Verdict),
            case_id,
            ..DiagnosticCoordinate::default()
        },
        detail,
    )])
    .expect("one facade diagnostic is present")
}
