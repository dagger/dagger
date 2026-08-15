//! Implementation-closure admission properties.

use dagger_sdk_completeness::{
    CanonicalSet, Digest, ImplementationClosureEvidence, ImplementationClosureObservation,
    ImplementationGateObservation, ImplementationGateOutcome, ModuleAuthoringScope,
    ModuleEvidenceDomain, ModuleEvidencePhase, NonEmptyText, Status, TargetDigest,
    assemble_implementation_closure, derive_module_authoring_report, derive_module_authoring_scope,
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
                let capability = scope
                    .mappings()
                    .values()
                    .next()
                    .expect("reviewed capability")
                    .capability_id
                    .clone();
                observation.claims.insert(
                    ModuleEvidenceDomain::CrossPlatform,
                    CanonicalSet::new([capability]),
                );
            }
            9 => observation.implementation_digest = digest(seed, 0x99),
            _ => unreachable!(),
        }

        let closure = assemble_implementation_closure(&scope, &observation);
        prop_assert_eq!(closure.is_ok(), mutation == 0);
        if let Ok(closure) = closure {
            prop_assert_eq!(closure.status_changes.len(), 110);
            prop_assert!(closure.remaining_blockers.is_empty());
            prop_assert!(closure.status_changes.values().all(|status| matches!(status, Status::Implemented | Status::IdiomaticEquivalent)));
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
fn report_reflects_complete_local_closure() {
    let (scope, closure) = valid_closure(9);
    let local = derive_module_authoring_report(&scope, Some(&closure))
        .expect("complete local evidence derives an honest report");
    assert!(matches!(
        local.implementation_closure,
        ModuleEvidencePhase::Passed { .. }
    ));
    assert_eq!(local.status_changes.len(), 110);
    assert!(local.blockers.is_empty());
}
