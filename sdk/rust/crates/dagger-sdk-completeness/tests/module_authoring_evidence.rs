//! Implementation-closure and exact-target sign-off admission properties.

use dagger_sdk_completeness::{
    CanonicalSet, CapabilityId, Digest, ExactTargetArtifactInput, ImplementationClosureEvidence,
    ImplementationClosureObservation, ImplementationGateObservation, ImplementationGateOutcome,
    ModuleAuthoringScope, ModuleEvidenceDomain, ModuleEvidencePhase, ModuleSignoffCaseObservation,
    ModuleSignoffCaseOutcome, ModuleSignoffExecutionShape, ModuleSignoffManifest,
    ModuleSignoffObservation, ModuleSignoffPhaseTimings, NonEmptyText, Status, TargetDigest,
    admit_module_signoff, assemble_implementation_closure, build_exact_target_signoff_artifact,
    build_module_signoff_manifest, derive_module_authoring_report, derive_module_authoring_scope,
    implementation_closure_claims, module_authoring_scope_input,
    required_implementation_closure_gates,
};
use dagger_sdk_engine::{
    CheckpointAction, CheckpointActionObservation, CheckpointActionOutcome,
    CheckpointGenerationDecision, CheckpointRecord, Sha256Digest,
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

    // Closure is admitted only from the complete passed engine-free gate set.
    #[test]
    fn property_29_implementation_closure_only_complete_local_evidence(
        seed in any::<u8>(),
        mutation in 0_u8..10,
    ) {
        let (scope, mut observation) = closure_observation(seed);
        match mutation {
            0 => {}
            1 => { observation.gates.pop(); }
            2 => observation.gates.push(observation.gates[0].clone()),
            3 => observation.gates[0].result = ImplementationGateOutcome::Failed {
                diagnostic: text("compiler property failed"),
            },
            4 => observation.gates[0].result = ImplementationGateOutcome::Skipped {
                reason: text("gate was not run"),
            },
            5 => observation.target_digest = target(seed.wrapping_add(1)),
            6 => observation.checkpoint.actions[0].outcome = CheckpointActionOutcome::Failed,
            7 => observation.checkpoint.actions[0].elapsed_millis = 0,
            8 => {
                let engine_claim = scope
                    .mappings()
                    .values()
                    .find(|mapping| mapping.minimum_evidence_domain == ModuleEvidenceDomain::ExactEngineSignoff)
                    .expect("reviewed sign-off claim")
                    .capability_id
                    .clone();
                observation.claims.insert(
                    ModuleEvidenceDomain::ExactEngineSignoff,
                    CanonicalSet::new([engine_claim]),
                );
            }
            9 => observation.implementation_digest = digest(seed, 0x99),
            _ => unreachable!(),
        }

        let closure = assemble_implementation_closure(&scope, &observation);
        prop_assert_eq!(closure.is_ok(), mutation == 0);
        if let Ok(closure) = closure {
            prop_assert_eq!(closure.status_changes.len(), 110);
            prop_assert_eq!(closure.signoff_blockers.len(), 1);
            prop_assert!(closure.status_changes.values().all(|status| matches!(status, Status::Implemented | Status::IdiomaticEquivalent)));
        }
    }

    // Sign-off admits one atomic exact-target run and rejects every partial or duplicated resource graph.
    #[test]
    fn property_30_sdk_signoff_exact_target_claim_bounded(
        seed in any::<u8>(),
        mutation in 0_u8..13,
    ) {
        let (scope, closure) = valid_closure(seed);
        let manifest = signoff_manifest(&scope, &closure, seed);
        let mut observation = signoff_observation(&manifest, seed);
        match mutation {
            0 => {}
            1 => { observation.cases.pop(); }
            2 => observation.cases[0].result = ModuleSignoffCaseOutcome::Failed {
                diagnostic: text("registration failed"),
            },
            3 => observation.cases[0].result = ModuleSignoffCaseOutcome::Skipped {
                reason: text("case not selected"),
            },
            4 => observation.execution_shape.artifact_materializations = 2,
            5 => observation.execution_shape.engine_starts = 2,
            6 => observation.execution_shape.rust_baseline_installs = 2,
            7 => observation.execution_shape.unrelated_builds = 1,
            8 => observation.artifact_digest = digest(seed, 0x81),
            9 => {
                let local = scope
                    .mappings()
                    .values()
                    .find(|mapping| mapping.minimum_evidence_domain != ModuleEvidenceDomain::ExactEngineSignoff)
                    .expect("reviewed local capability")
                    .capability_id
                    .clone();
                observation.capability_ids = CanonicalSet::new(
                    observation.capability_ids.iter().cloned().chain([local]),
                );
            }
            10 => observation.phase_timings.engine_start_millis = 0,
            11 => observation.cases.push(observation.cases[0].clone()),
            12 => observation.cases[0].case_digest = digest(seed, 0x82),
            _ => unreachable!(),
        }

        let admission = admit_module_signoff(&scope, &manifest, &observation);
        prop_assert_eq!(admission.rejection.is_none(), mutation == 0);
        if mutation == 0 {
            prop_assert!(admission.verdict_digest.is_some());
            prop_assert_eq!(admission.status_changes.len(), 1);
            prop_assert!(admission.blockers.is_empty());
        } else {
            prop_assert!(admission.verdict_digest.is_none());
            prop_assert!(admission.status_changes.is_empty());
            prop_assert_eq!(admission.blockers.len(), 111);
        }
    }
}

fn closure_observation(seed: u8) -> (ModuleAuthoringScope, ImplementationClosureObservation) {
    let target = target(seed);
    let scope =
        derive_module_authoring_scope(&module_authoring_scope_input(target.clone()), &target)
            .expect("reviewed module scope");
    let implementation_digest = digest(seed, 0x10);
    let checkpoint_digest = Sha256Digest::new(implementation_digest.as_str())
        .expect("completeness digest satisfies engine digest grammar");
    let gates = required_implementation_closure_gates()
        .into_iter()
        .enumerate()
        .map(|(index, gate)| ImplementationGateObservation {
            gate,
            result: ImplementationGateOutcome::Passed {
                evidence_digest: digest(seed, u8::try_from(index).unwrap_or_default()),
            },
        })
        .collect();
    let observation = ImplementationClosureObservation {
        format_version: dagger_sdk_completeness::ModuleAuthoringFormatVersion::current(),
        target_digest: target,
        mapping_digest: scope.mapping_digest().clone(),
        implementation_digest,
        generated_assets_digest: digest(seed, 0x20),
        checkpoint: CheckpointRecord {
            implementation_digest: checkpoint_digest,
            actions: vec![CheckpointActionObservation {
                action: CheckpointAction::CleanOutput,
                outcome: CheckpointActionOutcome::Passed,
                elapsed_millis: 1,
            }],
            generation: CheckpointGenerationDecision::ReuseChecked {
                manifest_digest: Sha256Digest::new(digest(seed, 0x21).as_str())
                    .expect("test digest satisfies engine grammar"),
            },
            deferred_signoff_exception: None,
        },
        gates,
        claims: implementation_closure_claims(&scope),
    };
    (scope, observation)
}

fn valid_closure(seed: u8) -> (ModuleAuthoringScope, ImplementationClosureEvidence) {
    let (scope, observation) = closure_observation(seed);
    let closure = assemble_implementation_closure(&scope, &observation)
        .expect("complete local evidence closes implementation");
    (scope, closure)
}

fn signoff_manifest(
    scope: &ModuleAuthoringScope,
    closure: &ImplementationClosureEvidence,
    seed: u8,
) -> ModuleSignoffManifest {
    let artifact = build_exact_target_signoff_artifact(ExactTargetArtifactInput {
        target_digest: closure.target_digest.clone(),
        dagger_revision: text("25300124ca110612edc09c43f89cb5fad6028170"),
        platform: text("linux/amd64"),
        engine_cli_input_digest: digest(seed, 0x30),
        go_runtime_digest: digest(seed, 0x31),
        rust_content_digest: digest(seed, 0x32),
        toolchain_digest: digest(seed, 0x33),
    })
    .expect("exact artifact input");
    build_module_signoff_manifest(scope, closure, artifact, digest(seed, 0x34))
        .expect("complete deferred sign-off manifest")
}

fn signoff_observation(manifest: &ModuleSignoffManifest, seed: u8) -> ModuleSignoffObservation {
    let cases = manifest
        .cases
        .values()
        .enumerate()
        .map(|(index, spec)| ModuleSignoffCaseObservation {
            case: spec.case,
            case_digest: spec.case_digest.clone(),
            workspace_digest: digest(
                seed,
                0x40_u8.wrapping_add(u8::try_from(index).unwrap_or_default()),
            ),
            elapsed_millis: u64::try_from(index).unwrap_or_default() + 1,
            result: ModuleSignoffCaseOutcome::Passed {
                observation_digest: digest(
                    seed,
                    0x50_u8.wrapping_add(u8::try_from(index).unwrap_or_default()),
                ),
            },
        })
        .collect();
    ModuleSignoffObservation {
        manifest_digest: dagger_sdk_completeness::canonical_digest(
            dagger_sdk_completeness::DigestDomain::ModuleAuthoring,
            manifest,
        )
        .expect("manifest is canonical"),
        artifact_digest: manifest.artifact.artifact_digest.clone(),
        implementation_closure_digest: manifest.implementation_closure_digest.clone(),
        generated_assets_digest: manifest.generated_assets_digest.clone(),
        runtime_digest: manifest.runtime_digest.clone(),
        execution_shape: ModuleSignoffExecutionShape {
            artifact_materializations: 1,
            engine_starts: 1,
            rust_baseline_installs: 1,
            unrelated_builds: 0,
        },
        phase_timings: ModuleSignoffPhaseTimings {
            artifact_build_or_import_millis: 1,
            engine_start_millis: 1,
            rust_install_millis: 1,
        },
        cases,
        capability_ids: CanonicalSet::new(
            manifest
                .cases
                .values()
                .flat_map(|case| case.capability_ids.iter().cloned()),
        ),
    }
}

fn target(seed: u8) -> TargetDigest {
    TargetDigest::new(digest(seed, 0x01))
}

fn digest(seed: u8, domain: u8) -> Digest {
    Digest::sha256([seed, domain])
}

fn text(value: &str) -> NonEmptyText {
    NonEmptyText::new(value).expect("test text is non-empty")
}

#[test]
fn signoff_inventory_is_closed_and_case_claims_are_disjoint() {
    let (scope, closure) = valid_closure(7);
    let manifest = signoff_manifest(&scope, &closure, 7);
    assert_eq!(manifest.cases.len(), 9);
    let claims = manifest
        .cases
        .values()
        .flat_map(|case| case.capability_ids.iter())
        .collect::<Vec<&CapabilityId>>();
    assert_eq!(claims.len(), 1);
    assert_eq!(
        closure.signoff_blockers,
        CanonicalSet::new(claims.into_iter().cloned())
    );
}

#[test]
fn report_keeps_engine_free_closure_distinct_from_deferred_signoff() {
    let (scope, closure) = valid_closure(9);
    let local = derive_module_authoring_report(&scope, Some(&closure), None)
        .expect("complete local evidence derives an honest report");
    assert!(matches!(
        local.implementation_closure,
        ModuleEvidencePhase::Passed { .. }
    ));
    assert_eq!(local.sdk_signoff, ModuleEvidencePhase::Unexecuted);
    assert_eq!(local.status_changes.len(), 110);
    assert_eq!(local.blockers.len(), 1);

    let manifest = signoff_manifest(&scope, &closure, 9);
    let observation = signoff_observation(&manifest, 9);
    let admission = admit_module_signoff(&scope, &manifest, &observation);
    let signed_off = derive_module_authoring_report(&scope, Some(&closure), Some(&admission))
        .expect("complete exact-target evidence derives a signed-off report");
    assert!(matches!(
        signed_off.sdk_signoff,
        ModuleEvidencePhase::Passed { .. }
    ));
    assert_eq!(signed_off.status_changes.len(), 111);
    assert!(signed_off.blockers.is_empty());
}
