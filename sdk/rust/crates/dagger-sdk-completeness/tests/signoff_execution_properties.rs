//! Installed-baseline, connector, isolated fan-out, and retry properties.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::OnceLock;

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const COMPLETENESS: &str = "../../completeness";
const CASES: u32 = 256;

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/signoff-execution.txt"
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
        let ledger: ResolvedLedger =
            decode_canonical(&checked_artifact("artifacts/ledger.json")).unwrap();
        let reviewed: ReviewedConformanceScope =
            decode_canonical(&checked_artifact("conformance-scope.json")).unwrap();
        let applicability: ConformanceScopeInput =
            decode_canonical(&checked_artifact("conformance-applicability.json")).unwrap();
        let scope = derive_conformance_scope(&ledger, &reviewed, applicability).unwrap();
        let assertions: AssertionCatalogInput =
            decode_canonical(&checked_artifact("conformance-assertions.json")).unwrap();
        let fixtures: FixtureRegistryInput =
            decode_canonical(&checked_artifact("conformance-fixtures.json")).unwrap();
        let cases: CaseCatalogInput =
            decode_canonical(&checked_artifact("conformance-cases.json")).unwrap();
        let assertions = compile_assertion_catalog(&scope, assertions).unwrap();
        let fixtures = compile_fixture_registry(fixtures).unwrap();
        compile_case_catalog(&scope, &assertions, &fixtures, cases).unwrap()
    })
}

fn commit(byte: u8) -> CommitSha {
    CommitSha::new(format!("{byte:02x}").repeat(20)).unwrap()
}

fn provenance_id(component: ArtifactComponent, seed: u8) -> ProvenanceId {
    ProvenanceId::new(format!("execution/{component:?}/{seed}").to_ascii_lowercase()).unwrap()
}

fn admitted_artifact(catalog: &CaseCatalog, seed: u8) -> AdmittedArtifact {
    let source_digest = match catalog.subject() {
        SubjectIdentity::SourceDigest(digest) => digest.clone(),
        SubjectIdentity::Revision(revision) => Digest::sha256(revision.as_str()),
    };
    let components = required_artifact_components()
        .into_iter()
        .map(|component| {
            (
                component,
                ArtifactComponentRecord {
                    component,
                    input_digest: Digest::sha256([seed, component as u8, 1]),
                    content_digest: Digest::sha256([seed, component as u8, 2]),
                    provenance: CanonicalSet::new([provenance_id(component, seed)]),
                },
            )
        })
        .collect::<BTreeMap<_, _>>();
    let toolchain_digests = required_artifact_toolchains()
        .into_iter()
        .map(|role| (role, Digest::sha256([seed, role as u8, 3])))
        .collect::<BTreeMap<_, _>>();
    let subject_revision = match catalog.subject() {
        SubjectIdentity::Revision(revision) => revision.clone(),
        SubjectIdentity::SourceDigest(_) => commit(seed.wrapping_add(1).max(1)),
    };
    let rust_dependency = RustSdkDependencyDescriptor {
        source: RustSdkDependencySource::Git,
        package: "dagger-sdk".to_owned(),
        url: "https://github.com/iw/dagger".to_owned(),
        revision: subject_revision.clone(),
    };
    let rust_dependency_descriptor_digest = rust_dependency.direct_digest().unwrap();
    let mut plan = ArtifactPlan {
        format_version: ArtifactFormatVersion::V1,
        target_descriptor_digest: catalog.target_digest().clone(),
        target_revision: commit(seed.max(1)),
        subject: SubjectRevisionObservation {
            repository: "https://github.com/iw/dagger".to_owned(),
            revision: subject_revision,
            focused_source_digest: source_digest.clone(),
            workspace_focused_source_digest: source_digest,
            reachable: true,
            clean: true,
            immutable: true,
        },
        platform: catalog.platform().clone(),
        engine_input_digest: Digest::sha256([seed, 4]),
        cli_input_digest: Digest::sha256([seed, 5]),
        go_runtime_digest: Digest::sha256([seed, 6]),
        rust_manifest_digest: Digest::sha256([seed, 7]),
        rust_descriptor_digest: Digest::sha256([seed, 8]),
        rust_dependency,
        rust_dependency_descriptor_digest,
        toolchain_digests,
        components,
        provenance_digest: Digest::sha256([]),
        materialization: ArtifactMaterialization::Build,
    };
    let provenance = artifact_provenance_document_without_validation(&plan);
    plan.provenance_digest =
        canonical_digest(DigestDomain::ConformanceSecurity, &provenance).unwrap();
    let provenance = artifact_provenance_document(&plan).unwrap();
    let payload = vec![seed, 0x42, 0x75, 0x6e, 0x64, 0x6c, 0x65];
    let manifest = artifact_manifest_for_payload(&plan, &payload).unwrap();
    let bundle = assemble_artifact_bundle(manifest.clone(), provenance, payload).unwrap();
    let component_builds = required_artifact_components()
        .into_iter()
        .map(|component| (component, 1))
        .collect::<BTreeMap<_, _>>();
    let mut events = vec![ArtifactEvent::ConstructionStarted];
    events.extend(
        component_builds
            .keys()
            .copied()
            .map(|component| ArtifactEvent::ComponentBuilt { component }),
    );
    events.extend([
        ArtifactEvent::PayloadExported,
        ArtifactEvent::ManifestVerified,
        ArtifactEvent::PayloadVerified,
        ArtifactEvent::ComponentsVerified,
        ArtifactEvent::ArtifactReady,
    ]);
    let verified_component_digests = manifest
        .components
        .iter()
        .map(|(component, record)| (*component, record.content_digest.clone()))
        .collect();
    admit_artifact(
        &plan,
        ArtifactObservation {
            strategy: ArtifactMaterialization::Build,
            manifest,
            bundle,
            events,
            counters: ArtifactCounters {
                construction: 1,
                imports: 0,
                component_builds,
                forbidden_work: CanonicalSet::default(),
            },
            verified_component_digests,
            elapsed_millis: 1,
        },
    )
    .unwrap()
}

fn artifact_provenance_document_without_validation(
    plan: &ArtifactPlan,
) -> ArtifactProvenanceDocument {
    ArtifactProvenanceDocument {
        format_version: plan.format_version,
        components: plan
            .components
            .iter()
            .map(|(component, record)| (*component, record.provenance.clone()))
            .collect(),
        toolchain_digests: plan.toolchain_digests.clone(),
    }
}

fn engine_identity(
    artifact: &AdmittedArtifact,
    engine_version: &DaggerVersion,
) -> ExactEngineIdentity {
    let manifest = artifact.bundle().manifest();
    let mut engine = ExactEngineIdentity {
        target_descriptor_digest: manifest.target_descriptor_digest.clone(),
        target_revision: manifest.target_revision.clone(),
        engine_version: engine_version.clone(),
        platform: manifest.platform.clone(),
        rust_manifest_digest: manifest.rust_manifest_digest.clone(),
        rust_descriptor_digest: manifest.rust_descriptor_digest.clone(),
        identity_digest: Digest::sha256([]),
    };
    engine.identity_digest = canonical_digest(
        DigestDomain::ConformanceInstalledBaseline,
        &(
            &engine.target_descriptor_digest,
            &engine.target_revision,
            &engine.engine_version,
            &engine.platform,
            &engine.rust_manifest_digest,
            &engine.rust_descriptor_digest,
        ),
    )
    .unwrap();
    engine
}

fn baseline_observation(
    artifact: &AdmittedArtifact,
    engine_version: &DaggerVersion,
    seed: u8,
) -> InstalledBaselineObservation {
    InstalledBaselineObservation {
        engine: engine_identity(artifact, engine_version),
        artifact_manifest_digest: artifact.manifest_digest().clone(),
        artifact_payload_digest: artifact.payload_digest().clone(),
        cli_digest: artifact.bundle().manifest().components[&ArtifactComponent::Cli]
            .content_digest
            .clone(),
        runner_image_digest: Digest::sha256([seed, 20]),
        installed_config_digest: Digest::sha256([seed, 21]),
        dependency: DependencyDescriptorObservation::Git {
            descriptor: artifact.bundle().manifest().rust_dependency.clone(),
            descriptor_digest: artifact
                .bundle()
                .manifest()
                .rust_dependency_descriptor_digest
                .clone(),
        },
        install_count: 1,
        clean_git_workspace: true,
        artifact_cli_only_on_path: true,
        host_cli_visible: false,
        stale_installed_config: false,
        service_starts_before_validation: 0,
        elapsed_millis: NonZeroMillis::new(1).unwrap(),
    }
}

fn admitted_baseline(
    artifact: &AdmittedArtifact,
    engine_version: &DaggerVersion,
    seed: u8,
) -> InstalledRustBaseline {
    admit_installed_rust_baseline(
        artifact,
        engine_version,
        baseline_observation(artifact, engine_version, seed),
    )
    .unwrap()
}

fn connector_observation(
    baseline: &InstalledRustBaseline,
    downloaded_cli: Digest,
    verified_download: bool,
    forbidden_fallback: bool,
) -> StableConnectorObservation {
    let (manifest, selected_source, selected_cli_digest, claim) = if verified_download {
        (
            DistributionManifestObservation::Available {
                manifest_digest: Digest::sha256("distribution manifest"),
                cli_digest: downloaded_cli.clone(),
                checksum_verified: true,
            },
            ConnectorCliSource::VerifiedDownload,
            downloaded_cli,
            DistributionClaim::VerifiedDownload,
        )
    } else {
        (
            DistributionManifestObservation::Unavailable {
                status: if forbidden_fallback {
                    CompatibilityHttpStatus::Forbidden
                } else {
                    CompatibilityHttpStatus::NotFound
                },
            },
            ConnectorCliSource::ArtifactPathFallback,
            baseline.cli_digest.clone(),
            DistributionClaim::CompatibilityPathFallback,
        )
    };
    StableConnectorObservation {
        explicit_local_cli_selected: false,
        path_cli_digest: baseline.cli_digest.clone(),
        host_cli_visible: false,
        manifest,
        selected_source,
        selected_cli_digest,
        claim,
        observed_engine_version: baseline.engine.engine_version.clone(),
        session_control_succeeded: true,
        authenticated_loopback_constructed: true,
        authenticated_query_succeeded: true,
        close_count: 1,
        child_reap_count: 1,
        elapsed_millis: NonZeroMillis::new(1).unwrap(),
    }
}

// Invariant: one exact packaged installation admits only a truthful production CLI result.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_09_exact_cli_install_distribution_honest(
        seed in any::<u8>(),
        mutation in 0_u8..24,
    ) {
        let catalog = checked_catalog();
        let artifact = admitted_artifact(catalog, seed.max(1));
        let version = DaggerVersion::new("v1.0.0-beta.10").unwrap();
        let mut baseline_input = baseline_observation(&artifact, &version, seed);
        match mutation {
            2 => baseline_input.install_count = 2,
            3 => baseline_input.dependency = DependencyDescriptorObservation::Path {
                descriptor_digest: Digest::sha256("ambient path"),
            },
            4 => baseline_input.host_cli_visible = true,
            5 => baseline_input.artifact_manifest_digest = Digest::sha256("other manifest"),
            6 => baseline_input.cli_digest = Digest::sha256("host cli"),
            7 => baseline_input.stale_installed_config = true,
            8 => baseline_input.service_starts_before_validation = 1,
            9 => baseline_input.clean_git_workspace = false,
            10 => baseline_input.artifact_cli_only_on_path = false,
            11 => baseline_input.engine.identity_digest = Digest::sha256("other engine"),
            12 => baseline_input.dependency = DependencyDescriptorObservation::Registry {
                descriptor_digest: Digest::sha256("registry substitution"),
            },
            13 => {
                if let DependencyDescriptorObservation::Git { descriptor, .. } =
                    &mut baseline_input.dependency
                {
                    descriptor.revision = commit(0xee);
                }
            }
            14 => {
                if let DependencyDescriptorObservation::Git {
                    descriptor_digest, ..
                } = &mut baseline_input.dependency
                {
                    *descriptor_digest = Digest::sha256("mutated descriptor bytes");
                }
            }
            _ => {}
        }
        let baseline = admit_installed_rust_baseline(&artifact, &version, baseline_input);
        if (2..=14).contains(&mutation) {
            prop_assert!(baseline.is_err());
            return Ok(());
        }
        let baseline = baseline.unwrap();
        let mut connector = connector_observation(
            &baseline,
            Digest::sha256([seed, 0xaa]),
            mutation == 1,
            seed % 2 == 0,
        );
        match mutation {
            15 => connector.explicit_local_cli_selected = true,
            16 => {
                connector.manifest = DistributionManifestObservation::Available {
                    manifest_digest: Digest::sha256("future manifest"),
                    cli_digest: connector.selected_cli_digest.clone(),
                    checksum_verified: false,
                };
            }
            17 => connector.claim = DistributionClaim::VerifiedDownload,
            18 => connector.close_count = 0,
            19 => connector.authenticated_query_succeeded = false,
            20 => connector.selected_source = ConnectorCliSource::Host,
            21 => connector.selected_cli_digest = Digest::sha256("substituted cli"),
            22 => connector.session_control_succeeded = false,
            23 => connector.authenticated_loopback_constructed = false,
            _ => {}
        }
        let accepted = admit_stable_connector(&baseline, connector).is_ok();
        prop_assert_eq!(accepted, mutation <= 1);
    }
}

fn safe_diagnostic(case_id: &SignoffCaseId) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::SignoffCaseFailed,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Case),
            case_id: Some(case_id.clone()),
            ..DiagnosticCoordinate::default()
        },
        "bounded case failure",
    )
}

fn attempt(
    binding: &CaseExecutionBinding,
    number: u32,
    outcome: CaseAttemptOutcome,
) -> CaseAttempt {
    let number = NonZeroCount::new(number).unwrap();
    CaseAttempt {
        attempt: number,
        execution_binding_digest: binding.execution_binding_digest.clone(),
        namespaces: derive_case_namespaces(binding, number).unwrap(),
        shared_work: AttemptSharedWorkCounters::default(),
        elapsed_millis: NonZeroMillis::new(1).unwrap(),
        outcome,
    }
}

fn passing_case_observation(
    case: &CaseDefinition,
    binding: &CaseExecutionBinding,
) -> SignoffCaseObservation {
    let outcome = CaseAttemptOutcome::Passed {
        observation_digest: Digest::sha256(case.id.as_str()),
    };
    SignoffCaseObservation {
        case_id: case.id.clone(),
        execution_binding_digest: binding.execution_binding_digest.clone(),
        attempts: vec![attempt(binding, 1, outcome.clone())],
        final_outcome: outcome,
        elapsed_millis: NonZeroMillis::new(1).unwrap(),
    }
}

fn valid_fanout(
    catalog: &CaseCatalog,
    artifact: &AdmittedArtifact,
    baseline: &InstalledRustBaseline,
    rotation: usize,
) -> FanoutObservation {
    let cases = catalog
        .cases()
        .values()
        .map(|case| {
            let binding = bind_case_execution(catalog, &case.id, artifact, baseline).unwrap();
            passing_case_observation(case, &binding)
        })
        .collect::<Vec<_>>();
    let mut completion_order = cases
        .iter()
        .map(|case| case.case_id.clone())
        .collect::<Vec<_>>();
    let completion_len = completion_order.len();
    completion_order.rotate_left(rotation % completion_len);
    FanoutObservation {
        catalog_digest: catalog.digest().clone(),
        artifact_manifest_digest: artifact.manifest_digest().clone(),
        artifact_payload_digest: artifact.payload_digest().clone(),
        engine_identity_digest: baseline.engine.identity_digest.clone(),
        baseline_digest: baseline.baseline_digest.clone(),
        maximum_concurrency: NonZeroCount::new(4).unwrap(),
        peak_concurrency: NonZeroCount::new(4).unwrap(),
        cases,
        completion_order,
        counters: FanoutCounters {
            engine_starts: 1,
            baseline_materializations: 1,
            additional_installs: 0,
            engine_stops: 1,
            child_reaps: 1,
            cross_case_state_accesses: 0,
            abandoned_siblings: 0,
            artifact_materializations: 0,
        },
        elapsed_millis: NonZeroMillis::new(1).unwrap(),
    }
}

// Invariant: arbitrary completion order cannot alter one-service lifecycle or namespace isolation.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_10_fanout_one_engine_one_baseline_isolated(
        seed in any::<u8>(),
        case_count in 1_usize..12,
        rotation in any::<usize>(),
        mutation in 0_u8..15,
    ) {
        let catalog_digest = Digest::sha256([seed, 1]);
        let manifest_digest = Digest::sha256([seed, 2]);
        let payload_digest = Digest::sha256([seed, 3]);
        let engine_digest = Digest::sha256([seed, 4]);
        let baseline_digest = Digest::sha256([seed, 5]);
        let expected = (0..case_count)
            .map(|index| SignoffCaseId::new(format!("case/{index:03}")).unwrap())
            .collect::<Vec<_>>();
        let mut completion_order = expected.clone();
        completion_order.rotate_left(rotation % case_count);
        let concurrency = u32::try_from(case_count.min(4)).unwrap();
        let mut topology = FanoutTopologyObservation {
            catalog_digest: catalog_digest.clone(),
            artifact_manifest_digest: manifest_digest.clone(),
            artifact_payload_digest: payload_digest.clone(),
            engine_identity_digest: engine_digest.clone(),
            baseline_digest: baseline_digest.clone(),
            maximum_concurrency: NonZeroCount::new(concurrency).unwrap(),
            peak_concurrency: NonZeroCount::new(concurrency).unwrap(),
            cases: expected
                .iter()
                .enumerate()
                .map(|(index, case_id)| FanoutCaseTopology {
                    case_id: case_id.clone(),
                    attempts: vec![CaseNamespaces {
                        workspace_digest: Digest::sha256(format!("{seed}/{index}/workspace")),
                        environment_digest: Digest::sha256(format!("{seed}/{index}/environment")),
                        cache_namespace_digest: Digest::sha256(format!("{seed}/{index}/cache")),
                        session_namespace_digest: Digest::sha256(format!("{seed}/{index}/session")),
                    }],
                })
                .collect(),
            completion_order,
            counters: FanoutCounters {
                engine_starts: 1,
                baseline_materializations: 1,
                additional_installs: 0,
                engine_stops: 1,
                child_reaps: 1,
                cross_case_state_accesses: 0,
                abandoned_siblings: 0,
                artifact_materializations: 0,
            },
        };
        match mutation {
            1 => topology.counters.engine_starts = 2,
            2 => topology.counters.baseline_materializations = 0,
            3 => topology.counters.cross_case_state_accesses = 1,
            4 => topology.counters.abandoned_siblings = 1,
            5 => topology.counters.additional_installs = 1,
            6 => topology.counters.artifact_materializations = 1,
            7 => topology.counters.child_reaps = 0,
            8 if case_count > 1 => topology.cases.swap(0, 1),
            9 if case_count > 1 => {
                topology.completion_order[0] = topology.completion_order[1].clone();
            }
            10 => {
                topology.peak_concurrency =
                    NonZeroCount::new(topology.maximum_concurrency.get() + 1).unwrap();
            }
            11 => topology.catalog_digest = Digest::sha256("other catalog"),
            12 => topology.engine_identity_digest = Digest::sha256("other engine"),
            13 => topology.cases[0].attempts.clear(),
            14 => {
                topology.cases[0].attempts[0].workspace_digest =
                    topology.cases[0].attempts[0].environment_digest.clone();
            }
            _ => {}
        }
        let accepted = admit_fanout_topology(
            &expected,
            &catalog_digest,
            &manifest_digest,
            &payload_digest,
            &engine_digest,
            &baseline_digest,
            &topology,
        )
        .is_ok();
        let no_effect = matches!(mutation, 8 | 9) && case_count == 1;
        prop_assert_eq!(accepted, mutation == 0 || no_effect);
    }
}

#[test]
fn complete_checked_catalog_fanout_uses_the_topology_admission() {
    let catalog = checked_catalog();
    let artifact = admitted_artifact(catalog, 0x2a);
    let version = DaggerVersion::new("v1.0.0-beta.10").unwrap();
    let baseline = admitted_baseline(&artifact, &version, 0x2a);
    let fanout = valid_fanout(catalog, &artifact, &baseline, 317);
    let admitted = admit_case_fanout(catalog, &artifact, &baseline, fanout).unwrap();
    assert_eq!(admitted.cases().len(), catalog.cases().len());
}

fn synthetic_binding(case: &CaseDefinition) -> CaseExecutionBinding {
    let mut binding = CaseExecutionBinding {
        case_id: case.id.clone(),
        case_digest: canonical_digest(DigestDomain::ConformanceCaseExecution, case).unwrap(),
        catalog_digest: Digest::sha256("catalog"),
        artifact_manifest_digest: Digest::sha256("manifest"),
        artifact_payload_digest: Digest::sha256("payload"),
        engine_identity_digest: Digest::sha256("engine"),
        baseline_digest: Digest::sha256("baseline"),
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

// Invariant: assertion failure is absorbing and infrastructure retry never rematerializes shared work.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_11_retry_history_absorbing_and_reused(mutation in 0_u8..14) {
        let catalog = checked_catalog();
        let case = catalog
            .cases()
            .values()
            .find(|case| case.program == CaseProgram::StableConnector)
            .unwrap();
        let binding = synthetic_binding(case);
        let passed = CaseAttemptOutcome::Passed {
            observation_digest: Digest::sha256("passed"),
        };
        let assertion = CaseAttemptOutcome::AssertionFailed {
            diagnostic: safe_diagnostic(&case.id),
        };
        let transport = CaseAttemptOutcome::InfrastructureFailed {
            class: ExecutionInfrastructureFailureClass::OrchestrationTransport,
            diagnostic: safe_diagnostic(&case.id),
        };
        let capacity = CaseAttemptOutcome::InfrastructureFailed {
            class: ExecutionInfrastructureFailureClass::RunnerCapacity,
            diagnostic: safe_diagnostic(&case.id),
        };
        let outcomes = match mutation {
            0 => vec![passed.clone()],
            1 => vec![transport.clone(), passed.clone()],
            2 => vec![assertion.clone()],
            3 => vec![transport.clone()],
            4 => vec![assertion.clone(), passed.clone()],
            5 => vec![capacity, passed.clone()],
            11 => vec![passed.clone(), passed.clone()],
            12 => vec![transport.clone(), transport.clone(), passed.clone()],
            13 => vec![transport.clone(), assertion.clone()],
            _ => vec![transport.clone(), passed.clone()],
        };
        let mut attempts = outcomes
            .iter()
            .cloned()
            .enumerate()
            .map(|(index, outcome)| attempt(&binding, (index + 1) as u32, outcome))
            .collect::<Vec<_>>();
        if mutation == 6 {
            attempts[1].attempt = NonZeroCount::new(1).unwrap();
        }
        if mutation == 7 {
            attempts[1].shared_work.engine_starts = 1;
        }
        if mutation == 8 {
            attempts[1].execution_binding_digest = Digest::sha256("changed binding");
        }
        let final_outcome = if mutation == 9 {
            assertion
        } else {
            outcomes.last().unwrap().clone()
        };
        let elapsed = if mutation == 10 {
            1
        } else {
            attempts.len() as u64
        };
        let observation = SignoffCaseObservation {
            case_id: case.id.clone(),
            execution_binding_digest: binding.execution_binding_digest.clone(),
            attempts,
            final_outcome,
            elapsed_millis: NonZeroMillis::new(elapsed).unwrap(),
        };
        let accepted = admit_case_observation(case, &binding, observation).is_ok();
        prop_assert_eq!(accepted, matches!(mutation, 0 | 1 | 2 | 3 | 13));
    }
}

#[test]
fn case_observation_wire_boundary_requires_canonical_known_fields() {
    let catalog = checked_catalog();
    let case = catalog
        .cases()
        .values()
        .find(|case| case.program == CaseProgram::StableConnector)
        .unwrap();
    let binding = synthetic_binding(case);
    let observation = passing_case_observation(case, &binding);
    let canonical = canonical_bytes(&observation).unwrap();
    assert_eq!(decode_case_observation(&canonical).unwrap(), observation);
    let mut value = serde_json::to_value(&observation).unwrap();
    value["ambient_command"] = serde_json::Value::String("dagger test".to_owned());
    assert!(decode_case_observation(&serde_json::to_vec(&value).unwrap()).is_err());
}

#[test]
fn fixed_program_registry_is_complete_typed_and_packaged() {
    let registry = compile_fixed_case_program_registry().unwrap();
    assert_eq!(registry.programs().len(), 63);
    assert_eq!(
        registry.programs().keys().cloned().collect::<BTreeSet<_>>(),
        required_fixed_programs()
    );
    assert!(registry.programs().values().all(|spec| {
        spec.sdk_source == FixedProgramSdkSource::ExactArtifactPackage
            && !matches!(spec.program, CaseProgram::IntegrationAssertion { .. })
    }));
    assert_eq!(
        registry
            .programs()
            .values()
            .filter(|spec| spec.boundary == FixedProgramBoundary::CommonHarnessSubject)
            .count(),
        17
    );
    assert_eq!(
        registry
            .programs()
            .values()
            .filter(|spec| spec.boundary == FixedProgramBoundary::PublicGeneratedCore)
            .count(),
        9
    );
    assert_eq!(
        registry
            .programs()
            .values()
            .filter(|spec| spec.boundary == FixedProgramBoundary::SharedBaselineCli)
            .count(),
        10
    );
    assert_eq!(
        registry
            .programs()
            .values()
            .filter(|spec| spec.boundary == FixedProgramBoundary::ProductionModuleDispatcher)
            .count(),
        9
    );
    assert_eq!(
        registry
            .programs()
            .values()
            .filter(|spec| spec.boundary == FixedProgramBoundary::PublicGeneratedClient)
            .count(),
        5
    );
    assert_eq!(
        registry
            .programs()
            .values()
            .filter(|spec| spec.boundary == FixedProgramBoundary::PublicRustClient)
            .count(),
        9
    );
    assert_ne!(registry.digest(), &Digest::sha256([]));
}
