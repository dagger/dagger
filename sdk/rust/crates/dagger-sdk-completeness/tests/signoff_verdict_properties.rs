//! Atomic exact-target run-plan and verdict properties.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::OnceLock;

use dagger_sdk_completeness::*;

#[path = "support/packaged_artifact.rs"]
mod packaged_artifact;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const COMPLETENESS: &str = "../../completeness";
const CASES: u32 = 256;
const REVIEWED_EXECUTIONS: u32 = 74;

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/signoff-verdict.txt"
        )))),
        ..Config::default()
    }
}

fn checked_artifact(path: &str) -> Vec<u8> {
    std::fs::read(format!(
        "{}/{COMPLETENESS}/{path}",
        env!("CARGO_MANIFEST_DIR")
    ))
    .unwrap()
}

fn checked_catalog() -> &'static CaseCatalog {
    static CATALOG: OnceLock<CaseCatalog> = OnceLock::new();
    CATALOG.get_or_init(|| {
        let assertions: AssertionCatalogInput =
            decode_canonical(&checked_artifact("conformance-assertions.json")).unwrap();
        let fixtures: FixtureRegistryInput =
            decode_canonical(&checked_artifact("conformance-fixtures.json")).unwrap();
        let cases: CaseCatalogInput =
            decode_canonical(&checked_artifact("conformance-cases.json")).unwrap();
        let assertions = compile_assertion_catalog(checked_scope(), assertions).unwrap();
        let fixtures = compile_fixture_registry(fixtures).unwrap();
        compile_case_catalog(checked_scope(), &assertions, &fixtures, cases).unwrap()
    })
}

fn checked_route_registry() -> &'static FacadeRouteRegistry {
    static ROUTES: OnceLock<FacadeRouteRegistry> = OnceLock::new();
    ROUTES.get_or_init(|| {
        let assertions: AssertionCatalogInput =
            decode_canonical(&checked_artifact("conformance-assertions.json")).unwrap();
        let fixtures: FixtureRegistryInput =
            decode_canonical(&checked_artifact("conformance-fixtures.json")).unwrap();
        let candidates: RustFirstConformanceManifestInput =
            decode_canonical(&checked_artifact("conformance-scenario-candidates.json")).unwrap();
        let registrations: RustScenarioRegistryInput =
            decode_canonical(&checked_artifact("conformance-scenario-realizations.json")).unwrap();
        let assertions = compile_assertion_catalog(checked_scope(), assertions).unwrap();
        let fixtures = compile_fixture_registry(fixtures).unwrap();
        let observable =
            compile_observable_fixture_program_registry(&assertions, &fixtures, checked_catalog())
                .unwrap();
        let runner = std::fs::read(format!(
            "{}/../../../../toolchains/rust-sdk-dev/testdata/scenario_conformance.rs",
            env!("CARGO_MANIFEST_DIR")
        ))
        .unwrap();
        let scenarios = compile_rust_scenario_registry(
            registrations,
            &candidates,
            checked_catalog(),
            &Digest::sha256(runner),
        )
        .unwrap();
        compile_facade_route_registry(checked_catalog(), &observable, &scenarios).unwrap()
    })
}

fn checked_scope() -> &'static ConformanceScope {
    static SCOPE: OnceLock<ConformanceScope> = OnceLock::new();
    SCOPE.get_or_init(|| {
        let ledger: ResolvedLedger =
            decode_canonical(&checked_artifact("artifacts/ledger.json")).unwrap();
        let reviewed: ReviewedConformanceScope =
            decode_canonical(&checked_artifact("conformance-scope.json")).unwrap();
        let applicability: ConformanceScopeInput =
            decode_canonical(&checked_artifact("conformance-applicability.json")).unwrap();
        derive_conformance_scope(&ledger, &reviewed, applicability).unwrap()
    })
}

fn commit(byte: u8) -> CommitSha {
    CommitSha::new(format!("{byte:02x}").repeat(20)).unwrap()
}

fn artifact_plan(catalog: &CaseCatalog) -> ArtifactPlan {
    let focused_source_digest = match catalog.subject() {
        SubjectIdentity::SourceDigest(digest) => digest.clone(),
        SubjectIdentity::Revision(revision) => Digest::sha256(revision.as_str()),
    };
    let subject_revision = commit(0x22);
    let seed = ArtifactPlanSeed {
        format_version: ConformanceFormatVersion::V1,
        target_descriptor_digest: catalog.target_digest().clone(),
        target_revision: commit(0x11),
        subject: SubjectRevisionObservation {
            repository: "https://github.com/iw/dagger".to_owned(),
            revision: subject_revision.clone(),
            focused_source_digest: focused_source_digest.clone(),
            workspace_focused_source_digest: focused_source_digest,
            reachable: true,
            clean: true,
            immutable: true,
        },
        platform: catalog.platform().clone(),
        engine_input_digest: Digest::sha256("engine-input"),
        cli_input_digest: Digest::sha256("cli-input"),
        go_runtime_digest: Digest::sha256("go-runtime"),
        rust_manifest_digest: Digest::sha256("rust-manifest"),
        toolchain_digests: required_artifact_toolchains()
            .into_iter()
            .map(|role| (role, Digest::sha256(format!("toolchain-{role:?}"))))
            .collect(),
        component_provenance: required_artifact_components()
            .into_iter()
            .map(|component| {
                (
                    component,
                    CanonicalSet::new([ProvenanceId::new(
                        format!("verdict/{component:?}").to_ascii_lowercase(),
                    )
                    .unwrap()]),
                )
            })
            .collect(),
    };
    let rust_dependency = RustSdkDependencyDescriptor {
        source: RustSdkDependencySource::Git,
        package: "dagger-sdk".to_owned(),
        url: "https://github.com/iw/dagger".to_owned(),
        revision: subject_revision,
    };
    let rust_dependency_descriptor_digest = rust_dependency.direct_digest().unwrap();
    seal_artifact_build_plan(
        seed,
        Digest::sha256("rust-descriptor"),
        rust_dependency,
        rust_dependency_descriptor_digest,
        required_artifact_components()
            .into_iter()
            .map(|component| (component, Digest::sha256([component as u8, 2])))
            .collect(),
    )
    .unwrap()
}

fn synthetic_binding(
    catalog: &CaseCatalog,
    case: &CaseDefinition,
    manifest: &Digest,
    payload: &Digest,
    engine: &Digest,
    baseline: &Digest,
) -> CaseExecutionBinding {
    let mut binding = CaseExecutionBinding {
        case_id: case.id.clone(),
        case_digest: canonical_digest(DigestDomain::ConformanceCaseExecution, case).unwrap(),
        catalog_digest: catalog.digest().clone(),
        artifact_manifest_digest: manifest.clone(),
        artifact_payload_digest: payload.clone(),
        engine_identity_digest: engine.clone(),
        baseline_digest: baseline.clone(),
        execution_binding_digest: Digest::sha256([]),
    };
    binding.execution_binding_digest = canonical_digest(
        DigestDomain::ConformanceCaseExecution,
        &(
            &binding.case_id,
            &binding.case_digest,
            &binding.catalog_digest,
            &binding.artifact_manifest_digest,
            &binding.artifact_payload_digest,
            &binding.engine_identity_digest,
            &binding.baseline_digest,
        ),
    )
    .unwrap();
    binding
}

fn passing_observation(
    case: &CaseDefinition,
    binding: &CaseExecutionBinding,
) -> SignoffCaseObservation {
    let outcome = CaseAttemptOutcome::Passed {
        observation_digest: Digest::sha256(case.id.as_str()),
    };
    let attempt_number = NonZeroCount::new(1).unwrap();
    SignoffCaseObservation {
        case_id: case.id.clone(),
        execution_binding_digest: binding.execution_binding_digest.clone(),
        attempts: vec![CaseAttempt {
            attempt: attempt_number,
            execution_binding_digest: binding.execution_binding_digest.clone(),
            namespaces: derive_case_namespaces(binding, attempt_number).unwrap(),
            shared_work: AttemptSharedWorkCounters::default(),
            elapsed_millis: NonZeroMillis::new(1).unwrap(),
            outcome: outcome.clone(),
        }],
        final_outcome: outcome,
        elapsed_millis: NonZeroMillis::new(1).unwrap(),
    }
}

#[derive(Clone)]
struct CheckedSignoff {
    catalog: &'static CaseCatalog,
    plan: SignoffRunPlan,
    bindings: BTreeMap<SignoffCaseId, CaseExecutionBinding>,
    manifest: Digest,
    payload: Digest,
    platform: Digest,
    security: Digest,
    engine: Digest,
    baseline: Digest,
    receipt: ArtifactImportReceipt,
    stable_connector: AdmittedStableConnector,
    bundle: VerifiedArtifactBundle,
    observation: SignoffObservation,
}

impl CheckedSignoff {
    fn context(&self) -> SignoffAdmissionContext<'_> {
        SignoffAdmissionContext {
            run_plan: &self.plan,
            case_catalog: self.catalog,
            case_bindings: &self.bindings,
            artifact_manifest_digest: &self.manifest,
            artifact_payload_digest: &self.payload,
            artifact_import_receipt: &self.receipt,
            platform_matrix_digest: &self.platform,
            security_report_digest: &self.security,
            engine_identity_digest: &self.engine,
            baseline_digest: &self.baseline,
            stable_connector: &self.stable_connector,
        }
    }
}

fn checked_signoff() -> CheckedSignoff {
    static SIGNOFF: OnceLock<CheckedSignoff> = OnceLock::new();
    SIGNOFF.get_or_init(build_checked_signoff).clone()
}

fn admitted_import(fixture: &CheckedSignoff) -> AdmittedArtifact {
    admit_artifact_import_receipt(
        &fixture.plan.artifact_plan,
        fixture.bundle.clone(),
        &fixture.receipt,
    )
    .unwrap()
}

fn facade_scanner_observation(fixture: &CheckedSignoff) -> ArtifactScannerObservation {
    ArtifactScannerObservation {
        format_version: ConformanceFormatVersion::V1,
        payload_digest: fixture.payload.clone(),
        scanner_provenance: ProvenanceId::new("image/trivy/0.69.3").unwrap(),
        scanner_version: SemverVersion::new("0.69.3").unwrap(),
        scanner_image_digest: Digest::sha256("scanner image"),
        database_provenance: ProvenanceId::new("source/trivy-db/reviewed").unwrap(),
        database_artifact_digest: Digest::sha256("database artifact"),
        database_content_digest: Digest::sha256("scanner database content"),
        database_metadata_digest: Digest::sha256("scanner database"),
        findings: Vec::new(),
        scanner_result_digest: Digest::sha256("scanner result"),
        elapsed: NonZeroMillis::new(1).unwrap(),
        artifact_input_count: 1,
        target_build_count: 0,
        source_scan_count: 0,
    }
}

fn facade_secret_report() -> SecretEvidenceReport {
    admit_secret_evidence(SecretEvidenceInput {
        canary_set_digest: Digest::sha256("facade canaries"),
        inspections: required_secret_inspection_domains()
            .into_iter()
            .map(|domain| SecretInspectionObservation {
                domain,
                inspected_bytes: 1,
                leaks: CanonicalSet::default(),
            })
            .collect(),
        sanitized_outputs: vec![SanitizedEvidence {
            digest: Digest::sha256("facade retained output"),
            byte_count: 1,
        }],
        packaged_artifacts: packaged_artifact::packaged_artifact_scan_bundle(),
        artifact_credentials_absent: true,
        verdict_credentials_absent: true,
        redaction_proven: true,
    })
    .unwrap()
}

fn facade_stable_connector(fixture: &CheckedSignoff) -> StableConnectorObservation {
    let cli_digest = fixture.bundle.manifest().components[&ArtifactComponent::Cli]
        .content_digest
        .clone();
    StableConnectorObservation {
        explicit_local_cli_selected: false,
        path_cli_digest: cli_digest.clone(),
        host_cli_visible: false,
        manifest: DistributionManifestObservation::Unavailable {
            status: CompatibilityHttpStatus::Forbidden,
        },
        selected_source: ConnectorCliSource::ArtifactPathFallback,
        selected_cli_digest: cli_digest,
        claim: DistributionClaim::CompatibilityPathFallback,
        observed_engine_version: DaggerVersion::new("v1.0.0-beta.10").unwrap(),
        session_control_succeeded: true,
        authenticated_loopback_constructed: true,
        authenticated_query_succeeded: true,
        close_count: 1,
        child_reap_count: 1,
        elapsed_millis: NonZeroMillis::new(1).unwrap(),
    }
}

fn raw_facade_observation(fixture: &CheckedSignoff) -> RawSignoffFacadeObservation {
    let stable_connector = facade_stable_connector(fixture);
    let stable_connector_digest = Digest::sha256(
        canonical_bytes(&stable_connector).expect("connector evidence is canonical"),
    );
    let cases = fixture
        .catalog
        .cases()
        .keys()
        .map(|case_id| {
            let route = &checked_route_registry().routes()[case_id];
            let definition = &fixture.catalog.cases()[case_id];
            let standalone_example_evidence = match definition.program {
                CaseProgram::StandaloneExample { example } => {
                    let (images, output_path, output_format): (
                        &[&str],
                        &str,
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
                    Some(FacadeStandaloneExampleEvidence {
                        fixture_digest: definition.fixture_digest.clone(),
                        resolved_images: images
                            .iter()
                            .map(|reference| {
                                (
                                    (*reference).to_owned(),
                                    Digest::sha256(reference.as_bytes()),
                                )
                            })
                            .collect(),
                        output_path: RepositoryRelativePath::new(output_path).unwrap(),
                        output_digest: Digest::sha256(case_id.as_str()),
                        output_size_bytes: 1,
                        output_format,
                        credential_uses: 0,
                        publication_attempts: 0,
                    })
                }
                _ => None,
            };
            let standalone_digest = standalone_example_evidence
                .as_ref()
                .map(|evidence| Digest::sha256(canonical_bytes(evidence).unwrap()));
            FacadeCaseObservation {
                case_id: case_id.clone(),
                program: route.program().to_owned(),
                boundary: route.boundary().to_owned(),
                execution_selector: route.execution_selector().to_owned(),
                executed: route.executed(),
                attempt_outcomes: vec![FacadeAttemptOutcome::Passed],
                observation_digest: Some(if route.program() == "stable-connector/" {
                    stable_connector_digest.clone()
                } else {
                    standalone_digest.unwrap_or_else(|| Digest::sha256(case_id.as_str()))
                }),
                standalone_example_evidence,
                elapsed_millis: 1,
            }
        })
        .collect::<Vec<_>>();
    let case_millis = cases.len() as u64;
    let installed_config_digest = Digest::sha256("installed config");
    let baseline_directory_digest = Digest::sha256("baseline directory");
    let mut baseline_input = Vec::new();
    baseline_input.extend_from_slice(installed_config_digest.as_str().as_bytes());
    baseline_input.push(0);
    baseline_input.extend_from_slice(baseline_directory_digest.as_str().as_bytes());
    baseline_input.push(0);
    baseline_input.extend_from_slice(&[1, 1, 0, 0]);
    baseline_input.extend_from_slice(&0_u32.to_be_bytes());
    let manifest = fixture.bundle.manifest();
    let mut engine_input = Vec::new();
    engine_input.extend_from_slice(fixture.plan.target_digest.0.as_str().as_bytes());
    engine_input.push(0);
    engine_input.extend_from_slice(manifest.target_revision.as_str().as_bytes());
    engine_input.push(0);
    engine_input.extend_from_slice(b"v1.0.0-beta.10");
    engine_input.push(0);
    engine_input.extend_from_slice(&canonical_bytes(manifest).unwrap());
    RawSignoffFacadeObservation {
        format_version: ConformanceFormatVersion::V1,
        target_digest: fixture.plan.target_digest.clone(),
        subject_revision: fixture.plan.subject_revision.clone(),
        case_catalog_digest: fixture.plan.case_catalog_digest.clone(),
        closure_bundle_digest: fixture.plan.closure_bundle_digest.clone(),
        platform_matrix_digest: fixture.platform.clone(),
        artifact_manifest_digest: fixture.manifest.clone(),
        artifact_payload_digest: fixture.payload.clone(),
        scanner_result_digest: Digest::sha256("scanner result"),
        scanner_observation: facade_scanner_observation(fixture),
        secret_report: facade_secret_report(),
        stable_connector,
        engine_observation_digest: Digest::sha256(engine_input),
        baseline_observation_digest: Digest::sha256(baseline_input),
        baseline_directory_digest,
        installed_config_digest,
        clean_git_workspace: true,
        artifact_cli_only_on_path: true,
        host_cli_visible: false,
        stale_installed_config: false,
        service_starts_before_validation: 0,
        dependency: DependencyDescriptorObservation::Git {
            descriptor: manifest.rust_dependency.clone(),
            descriptor_digest: manifest.rust_dependency_descriptor_digest.clone(),
        },
        verified_component_digests: manifest
            .components
            .iter()
            .map(|(component, record)| (*component, record.content_digest.clone()))
            .collect(),
        verified_rust_descriptor_digest: manifest.rust_descriptor_digest.clone(),
        verified_rust_dependency: manifest.rust_dependency.clone(),
        verified_rust_dependency_descriptor_digest: manifest
            .rust_dependency_descriptor_digest
            .clone(),
        artifact_import_receipt: fixture.receipt.clone(),
        artifact_import_receipt_digest: fixture.receipt.receipt_digest.clone(),
        runner_image_digest: Digest::sha256("runner image"),
        artifact_constructions: 0,
        artifact_imports: 1,
        engine_component_builds: 0,
        cli_component_builds: 0,
        go_runtime_component_builds: 0,
        rust_sdk_component_builds: 0,
        orchestration_engine_starts: 1,
        exact_target_engine_starts: 1,
        exact_target_engine_stops: 1,
        exact_target_child_reaps: 1,
        rust_baseline_materializations: 1,
        case_executions: REVIEWED_EXECUTIONS,
        closure_replays: 0,
        unrelated_actions: 0,
        external_publication_attempts: 0,
        cases,
        artifact_millis: 5,
        security_scan_millis: 5,
        engine_startup_millis: 5,
        rust_installation_millis: 5,
        case_execution_millis: case_millis,
        runnable_execution_millis: REVIEWED_EXECUTIONS as u64,
        cleanup_millis: 5,
        total_millis: case_millis + 25,
    }
}

fn facade_security_report(fixture: &CheckedSignoff) -> ArtifactSecurityReport {
    let secret = facade_secret_report();
    ArtifactSecurityReport {
        format_version: ConformanceFormatVersion::V1,
        artifact_manifest_digest: fixture.manifest.clone(),
        artifact_payload_digest: fixture.payload.clone(),
        artifact_import_receipt_digest: fixture.receipt.receipt_digest.clone(),
        rust_security_digest: Digest::sha256("rust security"),
        provenance_registry_digest: Digest::sha256("provenance registry"),
        scanner_image_digest: Digest::sha256("scanner image"),
        database_artifact_digest: Digest::sha256("database artifact"),
        database_content_digest: Digest::sha256("scanner database content"),
        database_metadata_digest: Digest::sha256("scanner database"),
        scanner_result_digest: Digest::sha256("scanner result"),
        vulnerability: VulnerabilityAdmission {
            artifact_payload_digest: fixture.payload.clone(),
            scanner_provenance: ProvenanceId::new("scanner/trivy").unwrap(),
            database_digest: Digest::sha256("scanner database content"),
            findings: CanonicalSet::default(),
            exceptions: CanonicalSet::default(),
            admission_digest: Digest::sha256("vulnerability admission"),
        },
        secret_report_digest: secret.report_digest,
        inspected_domains: secret.inspected_domains,
        scan_elapsed: NonZeroMillis::new(1).unwrap(),
        policy_elapsed: NonZeroMillis::new(1).unwrap(),
        report_digest: fixture.security.clone(),
    }
}

#[test]
fn raw_go_facade_is_translated_into_one_passing_rust_owned_verdict() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    assert_eq!(checked_route_registry().routes().len(), 675);
    assert_eq!(
        checked_route_registry().physical_executions(),
        REVIEWED_EXECUTIONS
    );
    let admitted = admit_signoff_facade_observation(
        &fixture.plan,
        fixture.catalog,
        checked_route_registry(),
        &artifact,
        &fixture.platform,
        &security,
        raw_facade_observation(&fixture),
    )
    .unwrap();
    assert_eq!(
        admitted.stable_connector().baseline_digest,
        admitted.baseline().baseline_digest
    );
    let context = SignoffAdmissionContext {
        run_plan: &fixture.plan,
        case_catalog: fixture.catalog,
        case_bindings: admitted.bindings(),
        artifact_manifest_digest: artifact.manifest_digest(),
        artifact_payload_digest: artifact.payload_digest(),
        artifact_import_receipt: &admitted.observation().artifact_import_receipt,
        platform_matrix_digest: &fixture.platform,
        security_report_digest: &fixture.security,
        engine_identity_digest: &admitted.baseline().engine.identity_digest,
        baseline_digest: &admitted.baseline().baseline_digest,
        stable_connector: admitted.stable_connector(),
    };
    let verdict = derive_atomic_signoff_verdict(&context, admitted.observation().clone());
    assert!(matches!(verdict.decision, VerdictDecision::Passed { .. }));
    assert_eq!(verdict.cases.len(), 675);
    assert_eq!(
        verdict.execution_counts.case_executions,
        REVIEWED_EXECUTIONS
    );
}

#[test]
fn raw_go_facade_rejects_connector_or_publication_substitution() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    for mutation in 0..9 {
        let mut raw = raw_facade_observation(&fixture);
        match mutation {
            0 => raw.stable_connector.session_control_succeeded = false,
            1 => raw.stable_connector.authenticated_loopback_constructed = false,
            2 => raw.stable_connector.authenticated_query_succeeded = false,
            3 => raw.stable_connector.selected_cli_digest = Digest::sha256("substituted cli"),
            4 => {
                raw.cases
                    .iter_mut()
                    .find(|case| case.program == "stable-connector/")
                    .unwrap()
                    .observation_digest = Some(Digest::sha256("substituted connector evidence"));
            }
            5 => raw.external_publication_attempts = 1,
            6..=8 => {
                let row = raw
                    .cases
                    .iter_mut()
                    .find(|case| case.standalone_example_evidence.is_some())
                    .unwrap();
                let evidence = row.standalone_example_evidence.as_mut().unwrap();
                match mutation {
                    6 => evidence.credential_uses = 1,
                    7 => evidence.resolved_images.clear(),
                    8 => {
                        evidence.output_format = match evidence.output_format {
                            FacadeStandaloneOutputFormat::Executable => {
                                FacadeStandaloneOutputFormat::OciGzip
                            }
                            FacadeStandaloneOutputFormat::OciGzip => {
                                FacadeStandaloneOutputFormat::Executable
                            }
                        }
                    }
                    _ => unreachable!(),
                }
                row.observation_digest = Some(Digest::sha256(canonical_bytes(evidence).unwrap()));
            }
            _ => unreachable!(),
        }
        assert!(
            admit_signoff_facade_observation(
                &fixture.plan,
                fixture.catalog,
                checked_route_registry(),
                &artifact,
                &fixture.platform,
                &security,
                raw,
            )
            .is_err(),
            "mutation {mutation} must fail closed"
        );
    }
}

#[test]
fn raw_go_facade_rejects_an_omitted_or_substituted_catalog_row() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    let mut raw = raw_facade_observation(&fixture);
    raw.cases.pop();
    assert!(
        admit_signoff_facade_observation(
            &fixture.plan,
            fixture.catalog,
            checked_route_registry(),
            &artifact,
            &fixture.platform,
            &security,
            raw,
        )
        .is_err()
    );
}

#[test]
fn raw_go_facade_rejects_substituted_security_engine_and_baseline_observations() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    for mutation in 0..16 {
        let mut raw = raw_facade_observation(&fixture);
        match mutation {
            0 => raw.scanner_result_digest = Digest::sha256("substituted scanner result"),
            1 => raw.engine_observation_digest = Digest::sha256("substituted engine"),
            2 => raw.baseline_directory_digest = Digest::sha256("substituted baseline"),
            3 => raw.secret_report.report_digest = Digest::sha256("substituted secret report"),
            4 => {
                raw.verified_component_digests.insert(
                    ArtifactComponent::Engine,
                    Digest::sha256("substituted engine bytes"),
                );
            }
            5 => {
                raw.verified_rust_descriptor_digest = Digest::sha256("substituted Rust descriptor")
            }
            6 => raw.verified_rust_dependency.revision = commit(0xee),
            7 => {
                raw.verified_rust_dependency_descriptor_digest =
                    Digest::sha256("substituted dependency descriptor")
            }
            8 => {
                raw.dependency = DependencyDescriptorObservation::Registry {
                    descriptor_digest: Digest::sha256("registry substitution"),
                }
            }
            9 => raw.clean_git_workspace = false,
            10 => raw.artifact_cli_only_on_path = false,
            11 => raw.host_cli_visible = true,
            12 => raw.stale_installed_config = true,
            13 => raw.service_starts_before_validation = 1,
            14 => raw.artifact_import_receipt_digest = Digest::sha256("substituted receipt"),
            15 => raw.artifact_import_receipt.import_count = 2,
            _ => unreachable!(),
        }
        assert!(
            admit_signoff_facade_observation(
                &fixture.plan,
                fixture.catalog,
                checked_route_registry(),
                &artifact,
                &fixture.platform,
                &security,
                raw,
            )
            .is_err()
        );
    }
}

#[test]
fn raw_go_facade_rejects_route_and_representative_substitution() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    for mutation in 0..4 {
        let mut raw = raw_facade_observation(&fixture);
        let row = raw.cases.first_mut().unwrap();
        match mutation {
            0 => row.program = "core-shape/scalar".to_owned(),
            1 => row.boundary = "public-generated-core".to_owned(),
            2 => row.execution_selector = "realization/substituted".to_owned(),
            3 => row.executed = !row.executed,
            _ => unreachable!(),
        }
        assert!(
            admit_signoff_facade_observation(
                &fixture.plan,
                fixture.catalog,
                checked_route_registry(),
                &artifact,
                &fixture.platform,
                &security,
                raw,
            )
            .is_err()
        );
    }
}

fn retryable_case_id(class: InfrastructureFailureClass) -> SignoffCaseId {
    checked_catalog()
        .cases()
        .values()
        .find(|case| case.retry.retryable.contains(&class))
        .expect("the checked catalog contains the reviewed retry policy")
        .id
        .clone()
}

fn replace_raw_attempts(
    raw: &mut RawSignoffFacadeObservation,
    case_id: &SignoffCaseId,
    outcomes: Vec<FacadeAttemptOutcome>,
    observation_digest: Option<Digest>,
) {
    let row = raw
        .cases
        .iter_mut()
        .find(|row| &row.case_id == case_id)
        .expect("the complete raw facade contains the selected case");
    let previous = row.elapsed_millis;
    row.elapsed_millis = u64::try_from(outcomes.len()).unwrap();
    row.attempt_outcomes = outcomes;
    row.observation_digest = observation_digest;
    raw.case_execution_millis = raw.case_execution_millis - previous + row.elapsed_millis;
    raw.total_millis = raw.total_millis - previous + row.elapsed_millis;
}

#[test]
fn raw_go_facade_admits_and_retains_an_allowed_two_attempt_history() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    let case_id = retryable_case_id(InfrastructureFailureClass::OrchestrationTransportLost);
    let mut raw = raw_facade_observation(&fixture);
    let observation_digest = raw
        .cases
        .iter()
        .find(|case| case.case_id == case_id)
        .and_then(|case| case.observation_digest.clone())
        .unwrap();
    replace_raw_attempts(
        &mut raw,
        &case_id,
        vec![
            FacadeAttemptOutcome::OrchestrationTransport,
            FacadeAttemptOutcome::Passed,
        ],
        Some(observation_digest.clone()),
    );

    let admitted = admit_signoff_facade_observation(
        &fixture.plan,
        fixture.catalog,
        checked_route_registry(),
        &artifact,
        &fixture.platform,
        &security,
        raw,
    )
    .unwrap();
    let case = admitted
        .observation()
        .cases
        .iter()
        .find(|case| case.case_id == case_id)
        .unwrap();
    assert_eq!(case.attempts.len(), 2);
    assert_eq!(case.attempts[0].attempt.get(), 1);
    assert_eq!(case.attempts[1].attempt.get(), 2);
    assert_ne!(case.attempts[0].namespaces, case.attempts[1].namespaces);
    assert!(matches!(
        case.attempts[0].outcome,
        CaseAttemptOutcome::InfrastructureFailed {
            class: ExecutionInfrastructureFailureClass::OrchestrationTransport,
            ..
        }
    ));
    assert_eq!(
        case.final_outcome,
        CaseAttemptOutcome::Passed { observation_digest }
    );
    assert_eq!(case.elapsed_millis.get(), 2);
}

#[test]
fn raw_go_facade_rejects_retry_after_assertion_failure() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    let case_id = retryable_case_id(InfrastructureFailureClass::OrchestrationTransportLost);
    let mut raw = raw_facade_observation(&fixture);
    replace_raw_attempts(
        &mut raw,
        &case_id,
        vec![
            FacadeAttemptOutcome::AssertionFailed,
            FacadeAttemptOutcome::Passed,
        ],
        Some(Digest::sha256("assertion-retry")),
    );

    assert!(
        admit_signoff_facade_observation(
            &fixture.plan,
            fixture.catalog,
            checked_route_registry(),
            &artifact,
            &fixture.platform,
            &security,
            raw,
        )
        .is_err()
    );
}

#[test]
fn raw_go_facade_rejects_disallowed_or_over_bound_retry_histories() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    let case_id = retryable_case_id(InfrastructureFailureClass::OrchestrationTransportLost);
    for outcomes in [
        vec![
            FacadeAttemptOutcome::ImmutableRemoteFetch,
            FacadeAttemptOutcome::Passed,
        ],
        vec![
            FacadeAttemptOutcome::OrchestrationTransport,
            FacadeAttemptOutcome::OrchestrationTransport,
            FacadeAttemptOutcome::Passed,
        ],
    ] {
        let mut raw = raw_facade_observation(&fixture);
        replace_raw_attempts(
            &mut raw,
            &case_id,
            outcomes,
            Some(Digest::sha256("inadmissible-retry")),
        );
        assert!(
            admit_signoff_facade_observation(
                &fixture.plan,
                fixture.catalog,
                checked_route_registry(),
                &artifact,
                &fixture.platform,
                &security,
                raw,
            )
            .is_err()
        );
    }
}

#[test]
fn raw_go_facade_binds_observation_identity_only_to_a_final_pass() {
    let fixture = checked_signoff();
    let artifact = admitted_import(&fixture);
    let security = facade_security_report(&fixture);
    let case_id = retryable_case_id(InfrastructureFailureClass::OrchestrationTransportLost);
    for (outcomes, digest) in [
        (vec![FacadeAttemptOutcome::Passed], None),
        (
            vec![FacadeAttemptOutcome::AssertionFailed],
            Some(Digest::sha256("failure-cannot-observe-a-pass")),
        ),
    ] {
        let mut raw = raw_facade_observation(&fixture);
        replace_raw_attempts(&mut raw, &case_id, outcomes, digest);
        assert!(
            admit_signoff_facade_observation(
                &fixture.plan,
                fixture.catalog,
                checked_route_registry(),
                &artifact,
                &fixture.platform,
                &security,
                raw,
            )
            .is_err()
        );
    }
}

fn build_checked_signoff() -> CheckedSignoff {
    let catalog = checked_catalog();
    let payload_bytes = b"exact retained OCI payload".to_vec();
    let platform = Digest::sha256("platform-matrix");
    let security = Digest::sha256("security-report");
    let engine = Digest::sha256("exact-engine");
    let baseline = Digest::sha256("installed-baseline");
    let artifact_plan = artifact_plan(catalog);
    let provenance = ArtifactProvenanceDocument {
        format_version: ArtifactFormatVersion::V1,
        components: artifact_plan
            .components
            .iter()
            .map(|(component, record)| (*component, record.provenance.clone()))
            .collect(),
        toolchain_digests: artifact_plan.toolchain_digests.clone(),
    };
    let artifact_manifest = artifact_manifest_for_payload(&artifact_plan, &payload_bytes).unwrap();
    let bundle = assemble_artifact_bundle(artifact_manifest, provenance, payload_bytes).unwrap();
    let manifest = bundle.manifest_digest().clone();
    let payload = bundle.manifest().payload_digest.clone();
    let artifact_plan = artifact_import_plan(&artifact_plan, &bundle).unwrap();
    let plan = assemble_signoff_run_plan(
        artifact_plan,
        Digest::sha256("closure-bundle"),
        catalog,
        checked_route_registry(),
        Digest::sha256("host-profile"),
        Digest::sha256("host-preflight"),
    )
    .unwrap();
    let receipt = artifact_import_receipt(
        &plan.artifact_plan,
        &bundle,
        ArtifactImportObservation {
            format_version: ArtifactFormatVersion::V1,
            events: vec![
                ArtifactEvent::BundleSupplied,
                ArtifactEvent::ManifestVerified,
                ArtifactEvent::PayloadVerified,
                ArtifactEvent::ComponentsVerified,
                ArtifactEvent::ContainerImported,
                ArtifactEvent::ArtifactReady,
            ],
            construction_count: 0,
            import_count: 1,
            component_build_counts: required_artifact_components()
                .into_iter()
                .map(|component| (component, 0))
                .collect(),
            forbidden_work_counts: forbidden_artifact_work_classes()
                .into_iter()
                .map(|work| (work, 0))
                .collect(),
            verified_component_digests: bundle
                .manifest()
                .components
                .iter()
                .map(|(component, record)| (*component, record.content_digest.clone()))
                .collect(),
            materialization_elapsed_millis: NonZeroMillis::new(5).unwrap(),
        },
    )
    .unwrap();
    let bindings = catalog
        .cases()
        .iter()
        .map(|(case_id, case)| {
            (
                case_id.clone(),
                synthetic_binding(catalog, case, &manifest, &payload, &engine, &baseline),
            )
        })
        .collect::<BTreeMap<_, _>>();
    let cases = catalog
        .cases()
        .iter()
        .map(|(case_id, case)| passing_observation(case, &bindings[case_id]))
        .collect::<Vec<_>>();
    let case_millis = u64::try_from(cases.len()).unwrap();
    let phase_timings = SignoffPhaseTimings {
        artifact: NonZeroMillis::new(5).unwrap(),
        engine_startup: NonZeroMillis::new(5).unwrap(),
        rust_installation: NonZeroMillis::new(5).unwrap(),
        security_scan: NonZeroMillis::new(5).unwrap(),
        case_execution: NonZeroMillis::new(case_millis).unwrap(),
        cleanup: NonZeroMillis::new(5).unwrap(),
        total: NonZeroMillis::new(case_millis + 25).unwrap(),
    };
    let engine_identity = ExactEngineIdentity {
        target_descriptor_digest: catalog.target_digest().clone(),
        target_revision: plan.artifact_plan.target_revision.clone(),
        engine_version: DaggerVersion::new("v1.0.0-beta.10").unwrap(),
        platform: catalog.platform().clone(),
        rust_manifest_digest: plan.artifact_plan.rust_manifest_digest.clone(),
        rust_descriptor_digest: plan.artifact_plan.rust_descriptor_digest.clone(),
        identity_digest: engine.clone(),
    };
    let installed_baseline = InstalledRustBaseline {
        baseline_digest: baseline.clone(),
        artifact_digest: Digest::sha256("portable-artifact"),
        artifact_manifest_digest: manifest.clone(),
        artifact_payload_digest: payload.clone(),
        engine: engine_identity,
        cli_digest: Digest::sha256("artifact-cli"),
        installed_config_digest: Digest::sha256("installed-config"),
        dependency_descriptor_digest: Digest::sha256("dependency-descriptor"),
        runner_image_digest: Digest::sha256("runner-image"),
    };
    let stable_connector = AdmittedStableConnector {
        baseline_digest: baseline.clone(),
        selected_cli_digest: installed_baseline.cli_digest.clone(),
        claim: DistributionClaim::CompatibilityPathFallback,
        observation_digest: Digest::sha256("stable-connector-observation"),
    };
    let observation = SignoffObservation {
        run_plan_digest: signoff_run_plan_digest(&plan, catalog).unwrap(),
        host_profile_digest: plan.host_profile_digest.clone(),
        host_preflight_digest: plan.preflight_digest.clone(),
        artifact_manifest_digest: manifest.clone(),
        artifact_payload_digest: payload.clone(),
        artifact_import_receipt: receipt.clone(),
        closure_bundle_digest: plan.closure_bundle_digest.clone(),
        platform_matrix_digest: platform.clone(),
        security_report_digest: security.clone(),
        engine_identity_digest: engine.clone(),
        baseline: installed_baseline,
        stable_connector: stable_connector.clone(),
        execution_counts: SignoffExecutionCounts {
            preflight_smoke_engine_starts: 1,
            orchestration_engine_starts: 1,
            artifact: ArtifactCounters {
                construction: 0,
                imports: 1,
                component_builds: required_artifact_components()
                    .into_iter()
                    .map(|component| (component, 0))
                    .collect(),
                forbidden_work: CanonicalSet::default(),
            },
            exact_target_engine_starts: 1,
            exact_target_engine_stops: 1,
            exact_target_child_reaps: 1,
            rust_baseline_materializations: 1,
            case_executions: REVIEWED_EXECUTIONS,
            closure_replays: 0,
            unrelated_actions: 0,
        },
        phase_timings,
        cases,
        claimed_capability_ids: CanonicalSet::new(catalog.capability_cases().keys().cloned()),
        platform_gate_passed: true,
        security_gate_passed: true,
        secret_canary_leaks: 0,
        forbidden_events: Vec::new(),
    };
    CheckedSignoff {
        catalog,
        plan,
        bindings,
        manifest,
        payload,
        platform,
        security,
        engine,
        baseline,
        receipt,
        stable_connector,
        bundle,
        observation,
    }
}

#[test]
fn canonical_release_handoff_round_trip_retains_evidence_only_scope() {
    let fixture = checked_signoff();
    let verdict = derive_atomic_signoff_verdict(&fixture.context(), fixture.observation.clone());
    let handoff = derive_release_handoff(&fixture.bundle, &verdict).unwrap();
    let bytes = encode_release_handoff(&handoff).unwrap();
    assert_eq!(decode_release_handoff(&bytes).unwrap(), handoff);
    assert_eq!(handoff.authority, ReleaseHandoffAuthority::EvidenceOnly);
    assert_eq!(handoff.platform, PlatformDescriptor::linux_amd64());
    assert_eq!(
        handoff.signoff_bundle_digest,
        *fixture.bundle.bundle_digest()
    );
    let rendered = render_release_handoff(&handoff);
    assert!(rendered.contains("Authority: `evidence-only`"));
    assert!(rendered.contains("does not authorize publication"));
}

#[test]
fn passed_verdict_derives_complete_neutral_feature_8_transitions_and_report() {
    let verdict = baseline_verdict();
    let transitions = derive_conformance_status_changes(checked_scope(), verdict).unwrap();
    assert_eq!(transitions.changes.len(), 1_103);
    let counts = transitions.changes.values().fold(
        BTreeMap::<Status, usize>::new(),
        |mut counts, values| {
            *counts.entry(values.status.clone()).or_default() += 1;
            counts
        },
    );
    assert_eq!(counts[&Status::Implemented], 634);
    assert_eq!(counts[&Status::IdiomaticEquivalent], 9);
    assert_eq!(counts[&Status::Inapplicable], 460);

    let report =
        derive_conformance_report(checked_scope(), None, None, None, Some(verdict), Some(true))
            .unwrap();
    assert_eq!(report.implementation, ConformancePhaseState::Missing);
    assert_eq!(report.native_platform, ConformancePhaseState::Missing);
    assert_eq!(report.security, ConformancePhaseState::Missing);
    assert_eq!(report.exact_engine, ConformancePhaseState::Passed);
    assert_eq!(report.reproducibility, ConformanceReproductionState::Clean);
    assert_eq!(
        report.artifact_manifest_digest.as_ref(),
        Some(&verdict.artifact_manifest_digest)
    );
    assert_eq!(report.remaining_blockers, 0);
    let rendered = render_conformance_report(&report);
    assert!(rendered.contains("| Implementation | missing |"));
    assert!(rendered.contains("| Exact engine | passed |"));
    assert!(rendered.contains("| Reproducibility | clean |"));
    assert!(rendered.contains("Total sign-off time:"));
    assert!(rendered.contains("`Inapplicable`"));
}

// The retained bundle, passing imported verdict, subject, and one-platform scope are conjunctive.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_25_release_handoff_preserves_exact_bytes_and_scope(
        mutation in 0_u8..7,
    ) {
        let fixture = checked_signoff();
        let mut verdict = derive_atomic_signoff_verdict(
            &fixture.context(),
            fixture.observation.clone(),
        );
        let mut bundle_bytes = fixture.bundle.bytes().to_vec();
        let expected_handoff = mutation == 0;
        let result = match mutation {
            0 => derive_release_handoff(&fixture.bundle, &verdict),
            1 => {
                verdict.decision = VerdictDecision::Failed {
                    diagnostics: ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
                        ConformanceDiagnosticCode::SignoffCaseFailed,
                        DiagnosticCoordinate { phase: Some(DiagnosticPhase::Case), ..DiagnosticCoordinate::default() },
                        "reviewed assertion failed",
                    )]).unwrap(),
                };
                derive_release_handoff(&fixture.bundle, &verdict)
            }
            2 => {
                verdict.execution_counts.artifact.imports = 0;
                verdict.execution_counts.artifact.construction = 1;
                derive_release_handoff(&fixture.bundle, &verdict)
            }
            3 => {
                verdict.subject_revision = commit(0x99);
                derive_release_handoff(&fixture.bundle, &verdict)
            }
            4 => {
                verdict.platform = PlatformDescriptor {
                    operating_system: OperatingSystem::Macos,
                    architecture: Architecture::Arm64,
                };
                derive_release_handoff(&fixture.bundle, &verdict)
            }
            5 => {
                let last = bundle_bytes.len() - 1;
                bundle_bytes[last] ^= 1;
                prop_assert!(decode_artifact_bundle(&bundle_bytes).is_err());
                Err(ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
                    ConformanceDiagnosticCode::SignoffReleaseHandoffInvalid,
                    DiagnosticCoordinate { phase: Some(DiagnosticPhase::Verdict), ..DiagnosticCoordinate::default() },
                    "retained bundle bytes are unavailable",
                )]).unwrap())
            }
            _ => {
                // Absence is represented before derivation: without a byte-owning verified bundle,
                // there is deliberately no API input from which a handoff could be created.
                let unavailable: Option<&VerifiedArtifactBundle> = None;
                prop_assert!(unavailable.is_none());
                Err(ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
                    ConformanceDiagnosticCode::SignoffReleaseHandoffInvalid,
                    DiagnosticCoordinate { phase: Some(DiagnosticPhase::Verdict), ..DiagnosticCoordinate::default() },
                    "retained bundle bytes are unavailable",
                )]).unwrap())
            }
        };
        prop_assert_eq!(result.is_ok(), expected_handoff);
        if let Ok(handoff) = result {
            prop_assert_eq!(handoff.authority, ReleaseHandoffAuthority::EvidenceOnly);
            prop_assert_eq!(handoff.platform, PlatformDescriptor::linux_amd64());
            prop_assert_eq!(handoff.signoff_bundle_digest, fixture.bundle.bundle_digest().clone());
        }
    }
}

fn is_passed(verdict: &AtomicSignoffVerdict) -> bool {
    matches!(verdict.decision, VerdictDecision::Passed { .. })
}

fn baseline_verdict() -> &'static AtomicSignoffVerdict {
    static VERDICT: OnceLock<AtomicSignoffVerdict> = OnceLock::new();
    VERDICT.get_or_init(|| {
        let fixture = checked_signoff();
        derive_atomic_signoff_verdict(&fixture.context(), fixture.observation.clone())
    })
}

#[test]
fn canonical_verdict_round_trip_rechecks_digest_and_renders_neutrally() {
    let fixture = checked_signoff();
    let verdict = derive_atomic_signoff_verdict(&fixture.context(), fixture.observation.clone());
    assert!(is_passed(&verdict));
    let bytes = encode_atomic_signoff_verdict(&verdict).unwrap();
    assert_eq!(decode_atomic_signoff_verdict(&bytes).unwrap(), verdict);
    let rendered = render_atomic_signoff_verdict(&verdict);
    assert!(rendered.contains("Decision: `passed`"));
    assert!(rendered.contains("## Closure domains"));
    assert!(rendered.contains("## Artifact component builds"));
    assert!(rendered.contains("## Case attempts"));
    assert!(!rendered.contains("Implemented"));

    let mut corrupted = verdict;
    corrupted.secret_canary_leaks = 1;
    assert!(matches!(
        encode_atomic_signoff_verdict(&corrupted),
        Err(SignoffDecodeError::VerdictDigestMismatch)
    ));
}

// Declaration order is irrelevant, while every retained semantic mutation remains digest-visible.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_23_verdict_binds_all_identities_counts_outcomes_timings(
        mutation in 0_u8..22,
        rotation in any::<usize>(),
        declaration_order_only in any::<bool>(),
    ) {
        let fixture = checked_signoff();
        let baseline_verdict = baseline_verdict();
        prop_assert!(is_passed(baseline_verdict));

        let mut changed = fixture.observation.clone();
        let case_count = changed.cases.len();
        changed.cases.rotate_left(rotation % case_count);
        if !declaration_order_only {
            match mutation {
                0 => changed.host_profile_digest = Digest::sha256("changed-host"),
                1 => changed.host_preflight_digest = Digest::sha256("changed-preflight"),
                2 => changed.artifact_manifest_digest = Digest::sha256("changed-manifest"),
                3 => changed.artifact_payload_digest = Digest::sha256("changed-payload"),
                4 => changed.closure_bundle_digest = Digest::sha256("changed-closure"),
                5 => changed.platform_matrix_digest = Digest::sha256("changed-platform"),
                6 => changed.security_report_digest = Digest::sha256("changed-security"),
                7 => changed.engine_identity_digest = Digest::sha256("changed-engine"),
                8 => changed.baseline.baseline_digest = Digest::sha256("changed-baseline"),
                9 => changed.execution_counts.orchestration_engine_starts = 2,
                10 => changed.execution_counts.artifact.construction = 2,
                11 => {
                    changed.phase_timings.artifact = NonZeroMillis::new(6).unwrap();
                    changed.phase_timings.total = NonZeroMillis::new(
                        changed.phase_timings.total.get() + 1,
                    ).unwrap();
                }
                12 => {
                    let first = changed.cases.first_mut().unwrap();
                    let outcome = CaseAttemptOutcome::Passed {
                        observation_digest: Digest::sha256("changed-case-outcome"),
                    };
                    first.attempts[0].outcome = outcome.clone();
                    first.final_outcome = outcome;
                }
                13 => changed.claimed_capability_ids = CanonicalSet::new([
                    CapabilityId::new("overbroad/capability").unwrap(),
                ]),
                14 => changed.platform_gate_passed = false,
                15 => changed.security_gate_passed = false,
                16 => changed.secret_canary_leaks = 1,
                17 => changed.forbidden_events.push(ForbiddenSignoffEvent::Distribution),
                18 => changed.execution_counts.case_executions += 1,
                19 => changed.run_plan_digest = Digest::sha256("changed-plan"),
                20 => changed.artifact_import_receipt.import_count = 2,
                _ => {
                    changed.stable_connector.observation_digest =
                        Digest::sha256("changed-stable-connector")
                }
            }
        }
        let changed_verdict = derive_atomic_signoff_verdict(&fixture.context(), changed);
        if declaration_order_only {
            prop_assert_eq!(&changed_verdict, baseline_verdict);
        } else {
            prop_assert_ne!(&changed_verdict.verdict_digest, &baseline_verdict.verdict_digest);
        }
    }
}

fn malformed_observation(mutation: u8, fixture: &mut CheckedSignoff) -> bool {
    match mutation {
        0 => return true,
        1 => {
            fixture.observation.cases.pop();
        }
        2 => {
            let first = fixture.observation.cases.first_mut().unwrap();
            first.attempts.clear();
        }
        3 => {
            let first = fixture.observation.cases.first_mut().unwrap();
            let diagnostic = ConformanceDiagnostic::new(
                ConformanceDiagnosticCode::SignoffCaseFailed,
                DiagnosticCoordinate {
                    phase: Some(DiagnosticPhase::Case),
                    case_id: Some(first.case_id.clone()),
                    ..DiagnosticCoordinate::default()
                },
                "reviewed assertion failed",
            );
            let outcome = CaseAttemptOutcome::AssertionFailed { diagnostic };
            first.attempts[0].outcome = outcome.clone();
            first.final_outcome = outcome;
        }
        4 => fixture.observation.run_plan_digest = Digest::sha256("stale-plan"),
        5 => fixture.observation.execution_counts.artifact.construction = 2,
        6 => {
            fixture
                .observation
                .execution_counts
                .exact_target_engine_starts = 2
        }
        7 => {
            fixture
                .observation
                .execution_counts
                .rust_baseline_materializations = 2
        }
        8 => fixture.observation.execution_counts.unrelated_actions = 1,
        9 => fixture.observation.platform_gate_passed = false,
        10 => fixture.observation.security_gate_passed = false,
        11 => fixture.observation.secret_canary_leaks = 1,
        12 => {
            fixture.observation.claimed_capability_ids =
                CanonicalSet::new([CapabilityId::new("overbroad/capability").unwrap()]);
        }
        13 => fixture.observation.execution_counts.closure_replays = 1,
        14 => fixture
            .observation
            .forbidden_events
            .extend([ForbiddenSignoffEvent::Distribution; 2]),
        15 => fixture.plan.artifact_plan.subject.clean = false,
        16 => fixture.plan.artifact_plan.subject.reachable = false,
        17 => {
            fixture
                .plan
                .network_policies
                .remove(&NetworkPolicyId::new("network/engine-only").unwrap());
        }
        18 => {
            fixture.observation.phase_timings.total = NonZeroMillis::new(1).unwrap();
        }
        19 => fixture.observation.baseline.engine.identity_digest = Digest::sha256("wrong-engine"),
        20 => fixture.observation.host_profile_digest = Digest::sha256("wrong-host"),
        21 => fixture.observation.artifact_payload_digest = Digest::sha256("wrong-payload"),
        22 => fixture.observation.security_report_digest = Digest::sha256("stale-security"),
        23 => fixture.observation.execution_counts.case_executions += 1,
        _ => {
            let duplicate = fixture.observation.cases[0].clone();
            fixture.observation.cases.push(duplicate);
        }
    }
    false
}

// The independent model has a single accepting conjunction: no injected gate defect.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_24_signoff_atomic_fail_closed(mutation in 0_u8..25) {
        let mut fixture = checked_signoff();
        let expected_pass = malformed_observation(mutation, &mut fixture);
        let verdict = derive_atomic_signoff_verdict(&fixture.context(), fixture.observation.clone());
        prop_assert_eq!(is_passed(&verdict), expected_pass);
        match verdict.decision {
            VerdictDecision::Passed { capability_ids } => {
                prop_assert!(expected_pass);
                let expected = BTreeSet::from_iter(
                    fixture.catalog.capability_cases().keys().cloned(),
                );
                prop_assert_eq!(
                    BTreeSet::from_iter(capability_ids.into_inner()),
                    expected,
                );
            }
            VerdictDecision::Failed { diagnostics } => {
                prop_assert!(!expected_pass);
                prop_assert!(!diagnostics.as_slice().is_empty());
            }
        }
    }
}
