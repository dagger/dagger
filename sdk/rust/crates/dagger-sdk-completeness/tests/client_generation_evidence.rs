//! Standalone-client implementation-closure and deferred sign-off properties.

use std::collections::BTreeSet;
use std::path::Path;

use dagger_sdk_completeness::{
    CanonicalSet, ClientClosureGateDisposition, ClientClosureGateObservation,
    ClientClosureGateOutcome, ClientDependencyScope, ClientEvidencePhase,
    ClientGenerationClosureEvidence, ClientGenerationClosureObservation,
    ClientGenerationEvidenceArtifact, ClientGenerationScope, ClientReportSection,
    ClientSignoffArtifactInput, ClientSignoffCaseObservation, ClientSignoffCaseOutcome,
    ClientSignoffExecutionCounts, ClientSignoffInventory, ClientSignoffObservation,
    ClientSignoffPhaseTimings, ClientSignoffRun, Digest, DigestDomain, FeatureId, NonEmptyText,
    Status, TargetDescriptor, TargetDigest, admit_client_generation_closure,
    build_client_signoff_artifact, build_client_signoff_inventory, canonical_bytes,
    canonical_digest, client_generation_scope_input, client_implementation_closure_claims,
    client_signoff_verdict_digest, derive_client_generation_report, derive_client_generation_scope,
    plan_client_feature_end_gate, required_client_closure_gates, validate_client_signoff_candidate,
};
use dagger_sdk_engine::{
    CheckpointAction, CheckpointActionObservation, CheckpointActionOutcome,
    CheckpointGenerationDecision, CheckpointPackage, CheckpointRecord, CheckpointTestTarget,
    ClientAssetDisposition, ClientCargoExpectation, ClientCheckpointRecord, ModuleProperty,
    Sha256Digest,
};
use proptest::prelude::*;

fn property_config() -> ProptestConfig {
    ProptestConfig {
        cases: 256,
        ..ProptestConfig::default()
    }
}

proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_26_implementation_closure_complete_matching_local_evidence(
        seed in any::<u8>(),
        mutation in 0_u8..15,
    ) {
        let (scope, mut observation) = closure_observation(seed);
        match mutation {
            0 => {}
            1 => { observation.gates.pop(); }
            2 => observation.gates.push(observation.gates[0].clone()),
            3 => observation.gates[0].result = ClientClosureGateOutcome::Failed {
                diagnostic: text("compiler property failed"),
            },
            4 => observation.gates[0].result = ClientClosureGateOutcome::Skipped {
                reason: text("gate did not execute"),
            },
            5 => observation.gates[0].observed_input_digest = digest(seed, 0x90),
            6 => observation.target_digest = target(seed.wrapping_add(1)),
            7 => observation.mapping_digest = digest(seed, 0x91),
            8 => observation.implementation_digest = digest(seed, 0x92),
            9 => observation.manifest_digest = digest(seed, 0x93),
            10 => observation.checkpoint.checkpoint.actions[0].outcome = CheckpointActionOutcome::Failed,
            11 => observation.checkpoint.checkpoint.actions[0].elapsed_millis = 0,
            12 => { observation.claims.remove(&dagger_sdk_completeness::ClientEvidenceDomain::SchemaProperty); }
            13 => observation.checkpoint.cargo.push(observation.checkpoint.cargo[0].clone()),
            14 => observation.gates[0].disposition = ClientClosureGateDisposition::Executed {
                elapsed_millis: 0,
                cargo_invocations: 0,
            },
            _ => unreachable!(),
        }

        let closure = admit_client_generation_closure(&scope, &observation);
        prop_assert_eq!(closure.is_ok(), mutation == 0);
        if let Ok(closure) = closure {
            prop_assert_eq!(closure.status_changes.len(), 23);
            prop_assert_eq!(closure.signoff_blockers.len(), 2);
            prop_assert!(closure.status_changes.values().all(|status| *status == Status::Implemented));
        }
    }

    #[test]
    fn property_27_sdk_signoff_inventory_bounded_reused_atomic(
        seed in any::<u8>(),
        mutation in 0_u8..17,
    ) {
        let (scope, closure) = valid_closure(seed);
        let inventory = signoff_inventory(&scope, &closure, seed);
        let mut observation = signoff_observation(&inventory, seed);
        match mutation {
            0 => {}
            1 => { observation.run.cases.pop(); }
            2 => observation.run.cases.push(observation.run.cases[0].clone()),
            3 => observation.run.cases[0].result = ClientSignoffCaseOutcome::Failed {
                diagnostic: text("local initialization failed"),
            },
            4 => observation.run.cases[0].result = ClientSignoffCaseOutcome::Skipped {
                reason: text("case was not selected"),
            },
            5 => observation.run.cases[1].workspace_digest = observation.run.cases[0].workspace_digest.clone(),
            6 => observation.run.execution_counts.artifact_materializations = 2,
            7 => observation.run.execution_counts.engine_builds = 2,
            8 => observation.run.execution_counts.cli_builds = 2,
            9 => observation.run.execution_counts.go_runtime_builds = 2,
            10 => observation.run.execution_counts.rust_content_builds = 2,
            11 => observation.run.execution_counts.engine_starts = 2,
            12 => observation.run.execution_counts.rust_baseline_installs = 2,
            13 => observation.run.execution_counts.implementation_closure_replays = 1,
            14 => observation.run.execution_counts.unrelated_actions = 1,
            15 => observation.run.phase_timings.engine_start_millis = 0,
            16 => observation.run.artifact_digest = digest(seed, 0x94),
            _ => unreachable!(),
        }
        observation.verdict_digest = client_signoff_verdict_digest(&observation.run)
            .expect("test run is canonical");

        let admission = validate_client_signoff_candidate(
            &scope,
            &closure,
            &inventory,
            &observation,
        );
        prop_assert_eq!(admission.rejection.is_none(), mutation == 0);
        if mutation == 0 {
            prop_assert!(admission.verdict_digest.is_some());
            prop_assert_eq!(admission.status_changes.len(), 2);
            prop_assert!(admission.blockers.is_empty());
        } else {
            prop_assert!(admission.verdict_digest.is_none());
            prop_assert!(admission.status_changes.is_empty());
            prop_assert_eq!(admission.blockers.len(), 2);
        }
    }
}

#[test]
fn report_keeps_local_closure_and_sdk_signoff_distinct() {
    let (scope, closure) = valid_closure(7);
    let report = derive_client_generation_report(&scope, Some(&closure), None)
        .expect("admitted closure produces an honest report");

    assert!(matches!(
        report.implementation_closure,
        ClientEvidencePhase::Passed { .. }
    ));
    assert_eq!(report.sdk_signoff, ClientEvidencePhase::Unexecuted);
    assert_eq!(
        report.dependency_scope,
        ClientDependencyScope::CorePlusOneBoundModule
    );
    assert_eq!(report.ownership_correction.to, FeatureId::Feature3);
    assert_eq!(report.status_changes.len(), 23);
    assert_eq!(
        report.blockers[&ClientReportSection::Initialization].len(),
        1
    );
    assert_eq!(report.blockers[&ClientReportSection::SdkSignoff].len(), 1);
    assert!(
        report
            .preserved_boundaries
            .iter()
            .all(|row| row.owner == FeatureId::Feature5)
    );
}

#[test]
fn feature_end_planner_reuses_only_current_passed_observations() {
    let (_, observation) = closure_observation(9);
    let current = observation
        .gates
        .iter()
        .map(|gate| (gate.gate, gate.expected_input_digest.clone()))
        .collect();
    let complete = plan_client_feature_end_gate(current, &observation.gates)
        .expect("complete current observations are reusable");
    assert!(complete.scheduled.is_empty());
    assert_eq!(complete.reused.len(), required_client_closure_gates().len());

    let mut stale = observation.gates;
    let stale_gate = stale[0].gate;
    stale[0].observed_input_digest = digest(9, 0xee);
    let current = stale
        .iter()
        .map(|gate| (gate.gate, gate.expected_input_digest.clone()))
        .collect();
    let plan = plan_client_feature_end_gate(current, &stale)
        .expect("stale evidence is scheduled rather than admitted");
    assert_eq!(plan.scheduled, CanonicalSet::new([stale_gate]));
}

#[test]
fn checked_client_generation_artifacts_are_canonical_and_current() {
    let (scope, observation) = current_feature_end_observation();
    let closure = admit_client_generation_closure(&scope, &observation)
        .expect("current feature-end evidence closes implementation");
    let report = derive_client_generation_report(&scope, Some(&closure), None)
        .expect("current closure produces honest report");
    let artifact = ClientGenerationEvidenceArtifact {
        format_version: dagger_sdk_completeness::ClientGenerationFormatVersion::current(),
        observation: observation.clone(),
        closure,
        deferred_signoff_cases: CanonicalSet::new(
            dagger_sdk_completeness::required_client_signoff_cases(),
        ),
    };
    assert_eq!(
        canonical_bytes(&observation).unwrap(),
        include_bytes!("../../../completeness/evidence/client-generation-closure-observation.json")
    );
    assert_eq!(
        canonical_bytes(&artifact).unwrap(),
        include_bytes!("../../../completeness/evidence/client-generation-closure.json")
    );
    assert_eq!(
        canonical_bytes(&report).unwrap(),
        include_bytes!("../../../completeness/artifacts/client-generation-report.json")
    );
}

#[test]
#[ignore = "explicitly refreshes reviewed client-generation evidence"]
fn refresh_checked_client_generation_artifacts() {
    let (scope, observation) = current_feature_end_observation();
    let closure = admit_client_generation_closure(&scope, &observation)
        .expect("current feature-end evidence closes implementation");
    let report = derive_client_generation_report(&scope, Some(&closure), None)
        .expect("current closure produces honest report");
    let artifact = ClientGenerationEvidenceArtifact {
        format_version: dagger_sdk_completeness::ClientGenerationFormatVersion::current(),
        observation: observation.clone(),
        closure,
        deferred_signoff_cases: CanonicalSet::new(
            dagger_sdk_completeness::required_client_signoff_cases(),
        ),
    };
    let completeness = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../completeness");

    std::fs::write(
        completeness.join("evidence/client-generation-closure-observation.json"),
        canonical_bytes(&observation).expect("observation is canonical"),
    )
    .expect("checked observation is writable");
    std::fs::write(
        completeness.join("evidence/client-generation-closure.json"),
        canonical_bytes(&artifact).expect("artifact is canonical"),
    )
    .expect("checked closure is writable");
    std::fs::write(
        completeness.join("artifacts/client-generation-report.json"),
        canonical_bytes(&report).expect("report is canonical"),
    )
    .expect("checked report is writable");
}

fn current_feature_end_observation() -> (ClientGenerationScope, ClientGenerationClosureObservation)
{
    let target_descriptor: TargetDescriptor = dagger_sdk_completeness::decode_canonical(
        include_bytes!("../../../completeness/target.json"),
    )
    .expect("checked target is canonical");
    let target = TargetDigest::new(
        canonical_digest(DigestDomain::Target, &target_descriptor)
            .expect("checked target has an identity"),
    );
    let scope =
        derive_client_generation_scope(&client_generation_scope_input(target.clone()), &target)
            .expect("reviewed client scope");
    let implementation_digest = Digest::sha256(
        [
            include_bytes!("../src/client_generation.rs").as_slice(),
            include_bytes!("../src/lib.rs").as_slice(),
            include_bytes!("../../dagger-sdk-engine/src/checkpoint.rs").as_slice(),
            include_bytes!("../../dagger-sdk-engine/src/client/project.rs").as_slice(),
            include_bytes!("../../../CLIENT_GENERATION.md").as_slice(),
            include_bytes!("../../../ARCHITECTURE.md").as_slice(),
            include_bytes!("../../../CONTRIBUTING.md").as_slice(),
        ]
        .concat(),
    );
    let catalog_digest = Digest::sha256(include_bytes!(
        "../../../completeness/artifacts/core-codegen-bindings.json"
    ));
    let manifest_digest = Digest::sha256(
        [
            include_bytes!("../../dagger-codegen/src/client/render.rs").as_slice(),
            include_bytes!("../../dagger-sdk-engine/src/client/project.rs").as_slice(),
        ]
        .concat(),
    );
    let prior_checkpoint =
        Digest::new("sha256:3b67f6cf332044a5c4f1794663ab996c9447b91828d26183f5b4595babd2eed4")
            .expect("recorded prior checkpoint identity is valid");
    let gates = required_client_closure_gates()
        .into_iter()
        .map(|gate| {
            let disposition = match gate {
                dagger_sdk_completeness::ClientClosureGate::ProjectReconciliation
                | dagger_sdk_completeness::ClientClosureGate::Publication
                | dagger_sdk_completeness::ClientClosureGate::DiagnosticSecurity
                | dagger_sdk_completeness::ClientClosureGate::CargoHygiene
                | dagger_sdk_completeness::ClientClosureGate::DerivedReporting
                | dagger_sdk_completeness::ClientClosureGate::CleanOutput => {
                    ClientClosureGateDisposition::Executed {
                        elapsed_millis: gate_elapsed_millis(gate),
                        cargo_invocations: gate_cargo_invocations(gate),
                    }
                }
                _ => ClientClosureGateDisposition::Reused {
                    prior_checkpoint_digest: prior_checkpoint.clone(),
                },
            };
            // A reused domain retains its own unchanged input identity even when a
            // sibling domain advances the aggregate implementation identity.
            let input = match &disposition {
                ClientClosureGateDisposition::Executed { .. } => canonical_digest(
                    DigestDomain::ClientGeneration,
                    &(&implementation_digest, gate),
                ),
                ClientClosureGateDisposition::Reused { .. } => {
                    canonical_digest(DigestDomain::ClientGeneration, &(&prior_checkpoint, gate))
                }
            }
            .expect("gate input is canonical");
            ClientClosureGateObservation {
                gate,
                expected_input_digest: input.clone(),
                observed_input_digest: input,
                result: ClientClosureGateOutcome::Passed {
                    evidence_digest: canonical_digest(
                        DigestDomain::ClientGeneration,
                        &(gate, &disposition, "passed"),
                    )
                    .expect("gate result is canonical"),
                },
                disposition,
            }
        })
        .collect();
    let actions = current_checkpoint_actions();
    let checkpoint = ClientCheckpointRecord {
        checkpoint: CheckpointRecord {
            implementation_digest: sha(&implementation_digest),
            actions: actions
                .iter()
                .map(|(action, elapsed_millis, _)| CheckpointActionObservation {
                    action: action.clone(),
                    outcome: CheckpointActionOutcome::Passed,
                    elapsed_millis: *elapsed_millis,
                })
                .collect(),
            generation: CheckpointGenerationDecision::ReuseChecked {
                manifest_digest: sha(&manifest_digest),
            },
            deferred_signoff_exception: None,
        },
        disposition: ClientAssetDisposition::CheckedGeneratedReused,
        asset_input_digest: sha(&manifest_digest),
        asset_output_digest: sha(&manifest_digest),
        cargo: actions
            .into_iter()
            .map(|(action, _, invocations)| ClientCargoExpectation {
                action,
                invocations,
            })
            .collect(),
    };
    let observation = ClientGenerationClosureObservation {
        format_version: dagger_sdk_completeness::ClientGenerationFormatVersion::current(),
        target_digest: target,
        mapping_digest: scope.mapping_digest().clone(),
        implementation_digest,
        catalog_digest,
        manifest_digest,
        checkpoint,
        fixture_baseline_materializations: 1,
        gates,
        claims: client_implementation_closure_claims(&scope),
    };
    (scope, observation)
}

fn current_checkpoint_actions() -> Vec<(CheckpointAction, u64, u32)> {
    let packages = BTreeSet::from([
        CheckpointPackage::DaggerCodegen,
        CheckpointPackage::DaggerSdk,
        CheckpointPackage::DaggerSdkEngine,
        CheckpointPackage::DaggerSdkCompleteness,
    ]);
    vec![
        (CheckpointAction::Format { packages }, 875, 1),
        (
            CheckpointAction::Check {
                package: CheckpointPackage::DaggerSdkEngine,
                all_features: true,
            },
            1_880,
            1,
        ),
        (
            CheckpointAction::Check {
                package: CheckpointPackage::DaggerSdkCompleteness,
                all_features: true,
            },
            5_950,
            1,
        ),
        (
            evidence_test_action(
                CheckpointPackage::DaggerSdkEngine,
                &[
                    "client_checkpoint_properties",
                    "client_project_properties",
                    "client_usability_properties",
                ],
                &[2, 3, 11, 12, 13, 14, 15, 16, 23, 25],
            ),
            32_150,
            7,
        ),
        (
            evidence_test_action(
                CheckpointPackage::DaggerSdkCompleteness,
                &[
                    "client_generation_documentation",
                    "client_generation_evidence",
                ],
                &[26, 27],
            ),
            1_320,
            1,
        ),
        (
            evidence_test_action(CheckpointPackage::DaggerSdk, &["source_policy"], &[]),
            310,
            1,
        ),
        (
            CheckpointAction::Clippy {
                packages: BTreeSet::from([
                    CheckpointPackage::DaggerSdkEngine,
                    CheckpointPackage::DaggerSdkCompleteness,
                ]),
            },
            5_750,
            1,
        ),
        (
            CheckpointAction::Rustdoc {
                packages: BTreeSet::from([
                    CheckpointPackage::DaggerSdkEngine,
                    CheckpointPackage::DaggerSdkCompleteness,
                ]),
            },
            14_080,
            1,
        ),
        (CheckpointAction::CleanOutput, 1, 0),
    ]
}

fn evidence_test_action(
    package: CheckpointPackage,
    targets: &[&str],
    properties: &[u8],
) -> CheckpointAction {
    CheckpointAction::Test {
        package,
        targets: targets
            .iter()
            .map(|target| CheckpointTestTarget::new(*target).unwrap())
            .collect(),
        properties: properties
            .iter()
            .map(|property| ModuleProperty::new(*property).unwrap())
            .collect(),
    }
}

const fn gate_elapsed_millis(gate: dagger_sdk_completeness::ClientClosureGate) -> u64 {
    match gate {
        dagger_sdk_completeness::ClientClosureGate::ProjectReconciliation
        | dagger_sdk_completeness::ClientClosureGate::Publication => 32_150,
        dagger_sdk_completeness::ClientClosureGate::DiagnosticSecurity => 310,
        dagger_sdk_completeness::ClientClosureGate::CargoHygiene => 28_535,
        dagger_sdk_completeness::ClientClosureGate::DerivedReporting => 1_320,
        dagger_sdk_completeness::ClientClosureGate::CleanOutput => 1,
        _ => 1,
    }
}

const fn gate_cargo_invocations(gate: dagger_sdk_completeness::ClientClosureGate) -> u32 {
    match gate {
        dagger_sdk_completeness::ClientClosureGate::CleanOutput => 0,
        dagger_sdk_completeness::ClientClosureGate::ProjectReconciliation
        | dagger_sdk_completeness::ClientClosureGate::Publication => 7,
        _ => 1,
    }
}

fn closure_observation(seed: u8) -> (ClientGenerationScope, ClientGenerationClosureObservation) {
    let target = target(seed);
    let scope =
        derive_client_generation_scope(&client_generation_scope_input(target.clone()), &target)
            .expect("reviewed client scope");
    let implementation_digest = digest(seed, 0x10);
    let manifest_digest = digest(seed, 0x20);
    let clean_output = CheckpointAction::CleanOutput;
    let gates = required_client_closure_gates()
        .into_iter()
        .enumerate()
        .map(|(index, gate)| {
            let input = digest(
                seed,
                0x30_u8.wrapping_add(u8::try_from(index).unwrap_or_default()),
            );
            ClientClosureGateObservation {
                gate,
                expected_input_digest: input.clone(),
                observed_input_digest: input,
                disposition: ClientClosureGateDisposition::Executed {
                    elapsed_millis: 1,
                    cargo_invocations: 0,
                },
                result: ClientClosureGateOutcome::Passed {
                    evidence_digest: digest(
                        seed,
                        0x50_u8.wrapping_add(u8::try_from(index).unwrap_or_default()),
                    ),
                },
            }
        })
        .collect();
    let checkpoint = ClientCheckpointRecord {
        checkpoint: CheckpointRecord {
            implementation_digest: sha(&implementation_digest),
            actions: vec![CheckpointActionObservation {
                action: clean_output.clone(),
                outcome: CheckpointActionOutcome::Passed,
                elapsed_millis: 1,
            }],
            generation: CheckpointGenerationDecision::ReuseChecked {
                manifest_digest: sha(&manifest_digest),
            },
            deferred_signoff_exception: None,
        },
        disposition: ClientAssetDisposition::CheckedGeneratedReused,
        asset_input_digest: sha(&digest(seed, 0x21)),
        asset_output_digest: sha(&manifest_digest),
        cargo: vec![ClientCargoExpectation {
            action: clean_output,
            invocations: 0,
        }],
    };
    let observation = ClientGenerationClosureObservation {
        format_version: dagger_sdk_completeness::ClientGenerationFormatVersion::current(),
        target_digest: target,
        mapping_digest: scope.mapping_digest().clone(),
        implementation_digest,
        catalog_digest: digest(seed, 0x22),
        manifest_digest,
        checkpoint,
        fixture_baseline_materializations: 1,
        gates,
        claims: client_implementation_closure_claims(&scope),
    };
    (scope, observation)
}

fn valid_closure(seed: u8) -> (ClientGenerationScope, ClientGenerationClosureEvidence) {
    let (scope, observation) = closure_observation(seed);
    let closure = admit_client_generation_closure(&scope, &observation)
        .expect("complete local evidence closes client implementation");
    (scope, closure)
}

fn signoff_inventory(
    scope: &ClientGenerationScope,
    closure: &ClientGenerationClosureEvidence,
    seed: u8,
) -> ClientSignoffInventory {
    let artifact = build_client_signoff_artifact(ClientSignoffArtifactInput {
        target_digest: closure.target_digest.clone(),
        platform: text("linux/amd64"),
        engine_cli_input_digest: digest(seed, 0x60),
        go_runtime_digest: digest(seed, 0x61),
        rust_manifest_digest: digest(seed, 0x62),
        rust_descriptor_digest: digest(seed, 0x63),
        rust_content_digest: digest(seed, 0x64),
        toolchain_digest: digest(seed, 0x65),
    })
    .expect("exact artifact inputs");
    build_client_signoff_inventory(scope, closure, artifact, digest(seed, 0x66))
        .expect("complete local closure produces deferred inventory")
}

fn signoff_observation(inventory: &ClientSignoffInventory, seed: u8) -> ClientSignoffObservation {
    let cases = inventory
        .cases
        .values()
        .enumerate()
        .map(|(index, spec)| ClientSignoffCaseObservation {
            case: spec.case,
            case_digest: spec.case_digest.clone(),
            workspace_digest: digest(
                seed,
                0x70_u8.wrapping_add(u8::try_from(index).unwrap_or_default()),
            ),
            elapsed_millis: u64::try_from(index).unwrap_or_default() + 1,
            result: ClientSignoffCaseOutcome::Passed {
                observation_digest: digest(
                    seed,
                    0x80_u8.wrapping_add(u8::try_from(index).unwrap_or_default()),
                ),
            },
        })
        .collect();
    let run = ClientSignoffRun {
        inventory_digest: dagger_sdk_completeness::canonical_digest(
            dagger_sdk_completeness::DigestDomain::ClientGeneration,
            inventory,
        )
        .expect("inventory is canonical"),
        artifact_digest: inventory.artifact.artifact_digest.clone(),
        implementation_closure_digest: inventory.implementation_closure_digest.clone(),
        rust_baseline_digest: inventory.rust_baseline_digest.clone(),
        execution_counts: ClientSignoffExecutionCounts {
            artifact_materializations: 1,
            engine_builds: 1,
            cli_builds: 1,
            go_runtime_builds: 1,
            rust_content_builds: 1,
            engine_starts: 1,
            rust_baseline_installs: 1,
            implementation_closure_replays: 0,
            unrelated_actions: 0,
        },
        phase_timings: ClientSignoffPhaseTimings {
            artifact_build_or_import_millis: 1,
            engine_start_millis: 1,
            rust_install_millis: 1,
        },
        cases,
    };
    let verdict_digest = client_signoff_verdict_digest(&run).expect("run is canonical");
    ClientSignoffObservation {
        run,
        verdict_digest,
    }
}

fn target(seed: u8) -> TargetDigest {
    TargetDigest::new(digest(seed, 0x01))
}

fn digest(seed: u8, domain: u8) -> Digest {
    Digest::sha256([seed, domain])
}

fn sha(digest: &Digest) -> Sha256Digest {
    Sha256Digest::new(digest.as_str()).expect("completeness digest satisfies engine grammar")
}

fn text(value: &str) -> NonEmptyText {
    NonEmptyText::new(value).expect("test text is non-empty")
}
