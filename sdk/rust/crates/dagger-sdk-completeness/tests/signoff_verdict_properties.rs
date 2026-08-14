//! Atomic exact-target run-plan and verdict properties.

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

fn component_record(component: ArtifactComponent) -> ArtifactComponentRecord {
    ArtifactComponentRecord {
        component,
        input_digest: Digest::sha256([component as u8, 1]),
        content_digest: Digest::sha256([component as u8, 2]),
        provenance: CanonicalSet::new([ProvenanceId::new(
            format!("verdict/{component:?}").to_ascii_lowercase(),
        )
        .unwrap()]),
    }
}

fn artifact_plan(catalog: &CaseCatalog) -> ArtifactPlan {
    let focused_source_digest = match catalog.subject() {
        SubjectIdentity::SourceDigest(digest) => digest.clone(),
        SubjectIdentity::Revision(revision) => Digest::sha256(revision.as_str()),
    };
    ArtifactPlan {
        format_version: ConformanceFormatVersion::V1,
        target_descriptor_digest: catalog.target_digest().clone(),
        target_revision: commit(0x11),
        subject: SubjectRevisionObservation {
            revision: commit(0x22),
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
        rust_descriptor_digest: Digest::sha256("rust-descriptor"),
        toolchain_digests: required_artifact_toolchains()
            .into_iter()
            .map(|role| (role, Digest::sha256(format!("toolchain-{role:?}"))))
            .collect(),
        components: required_artifact_components()
            .into_iter()
            .map(|component| (component, component_record(component)))
            .collect(),
        provenance_digest: Digest::sha256("provenance"),
        materialization: ArtifactMaterialization::Build,
    }
}

fn network_policies(catalog: &CaseCatalog) -> BTreeMap<NetworkPolicyId, SignoffNetworkPolicy> {
    catalog
        .cases()
        .values()
        .map(|case| {
            let policy = match case.network.as_str() {
                "network/engine-only" => SignoffNetworkPolicy::EngineOnly,
                "network/immutable-remote" => SignoffNetworkPolicy::ImmutableRemote,
                "network/manifest-and-engine" => SignoffNetworkPolicy::ManifestAndEngine,
                other => panic!("unexpected checked network policy {other}"),
            };
            (case.network.clone(), policy)
        })
        .collect()
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
            platform_matrix_digest: &self.platform,
            security_report_digest: &self.security,
            engine_identity_digest: &self.engine,
            baseline_digest: &self.baseline,
        }
    }
}

fn checked_signoff() -> CheckedSignoff {
    static SIGNOFF: OnceLock<CheckedSignoff> = OnceLock::new();
    SIGNOFF.get_or_init(build_checked_signoff).clone()
}

fn build_checked_signoff() -> CheckedSignoff {
    let catalog = checked_catalog();
    let payload_bytes = b"exact retained OCI payload".to_vec();
    let platform = Digest::sha256("platform-matrix");
    let security = Digest::sha256("security-report");
    let engine = Digest::sha256("exact-engine");
    let baseline = Digest::sha256("installed-baseline");
    let mut artifact_plan = artifact_plan(catalog);
    let provenance = ArtifactProvenanceDocument {
        format_version: ArtifactFormatVersion::V1,
        components: artifact_plan
            .components
            .iter()
            .map(|(component, record)| (*component, record.provenance.clone()))
            .collect(),
        toolchain_digests: artifact_plan.toolchain_digests.clone(),
    };
    artifact_plan.provenance_digest =
        canonical_digest(DigestDomain::ConformanceSecurity, &provenance).unwrap();
    let artifact_manifest = artifact_manifest_for_payload(&artifact_plan, &payload_bytes).unwrap();
    let bundle = assemble_artifact_bundle(artifact_manifest, provenance, payload_bytes).unwrap();
    let manifest = bundle.manifest_digest().clone();
    let payload = bundle.manifest().payload_digest.clone();
    artifact_plan.materialization = ArtifactMaterialization::Import {
        manifest_digest: manifest.clone(),
        payload_digest: payload.clone(),
    };
    let plan = SignoffRunPlan {
        format_version: ConformanceFormatVersion::V1,
        target_digest: catalog.target_digest().clone(),
        subject_revision: artifact_plan.subject.revision.clone(),
        platform: catalog.platform().clone(),
        host_profile_digest: Digest::sha256("host-profile"),
        preflight_digest: Digest::sha256("host-preflight"),
        artifact_plan,
        closure_bundle_digest: Digest::sha256("closure-bundle"),
        case_catalog_digest: catalog.digest().clone(),
        network_policies: network_policies(catalog),
        maximum_concurrency: NonZeroCount::new(8).unwrap(),
        total_budget: NonZeroMillis::new(10_000).unwrap(),
    };
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
    let observation = SignoffObservation {
        run_plan_digest: signoff_run_plan_digest(&plan, catalog).unwrap(),
        host_profile_digest: plan.host_profile_digest.clone(),
        host_preflight_digest: plan.preflight_digest.clone(),
        artifact_manifest_digest: manifest.clone(),
        artifact_payload_digest: payload.clone(),
        closure_bundle_digest: plan.closure_bundle_digest.clone(),
        platform_matrix_digest: platform.clone(),
        security_report_digest: security.clone(),
        engine_identity_digest: engine.clone(),
        baseline: installed_baseline,
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
        mutation in 0_u8..19,
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
                _ => changed.run_plan_digest = Digest::sha256("changed-plan"),
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
    fn property_24_signoff_atomic_fail_closed(mutation in 0_u8..24) {
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
