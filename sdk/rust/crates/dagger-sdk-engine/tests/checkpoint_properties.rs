//! Engine-free checkpoint planning and observation properties.

use std::collections::BTreeSet;

use dagger_codegen::module::RegenerationClass;
use dagger_sdk_engine::{
    CheckpointAction, CheckpointActionObservation, CheckpointActionOutcome,
    CheckpointGenerationDecision, CheckpointObservation, CheckpointPackage, CheckpointProposal,
    CheckpointRequest, DeferredSignoffException, ForbiddenCheckpointBoundary, ModuleProperty,
    Sha256Digest, plan_checkpoint, record_checkpoint,
};
use proptest::prelude::*;

fn property_config() -> ProptestConfig {
    ProptestConfig {
        cases: 256,
        ..ProptestConfig::default()
    }
}

fn digest(seed: u8) -> Sha256Digest {
    Sha256Digest::new(format!("sha256:{seed:02x}{:02x}{}", 0x28, "00".repeat(30)))
        .expect("test digest is canonical")
}

fn valid_request(seed: u8) -> CheckpointRequest {
    let package = match seed % 5 {
        0 => CheckpointPackage::DaggerSdkMacros,
        1 => CheckpointPackage::DaggerCodegen,
        2 => CheckpointPackage::DaggerSdk,
        3 => CheckpointPackage::DaggerSdkEngine,
        _ => CheckpointPackage::DaggerSdkCompleteness,
    };
    CheckpointRequest {
        implementation_digest: digest(seed),
        proposals: vec![
            CheckpointProposal::Action {
                action: CheckpointAction::Check {
                    package,
                    all_features: true,
                },
            },
            CheckpointProposal::Action {
                action: CheckpointAction::Test {
                    package,
                    targets: BTreeSet::new(),
                    properties: BTreeSet::from([
                        ModuleProperty::new(seed % 32 + 1).expect("bounded property")
                    ]),
                },
            },
            CheckpointProposal::Action {
                action: CheckpointAction::GeneratedAssetDrift,
            },
        ],
        generation: CheckpointGenerationDecision::ReuseChecked {
            manifest_digest: digest(seed.wrapping_add(1)),
        },
        deferred_signoff_exception: None,
    }
}

proptest! {
    #![proptest_config(property_config())]

    // A local plan is admitted only when every observable boundary remains Rust-owned and engine-free.
    #[test]
    fn property_28_local_checkpoints_observably_engine_free_scoped(
        seed in any::<u8>(),
        mutation in 0_u8..10,
        forbidden in 0_u8..6,
    ) {
        let mut request = valid_request(seed);
        match mutation {
            0 => {}
            1 => request.proposals.clear(),
            2 => request.proposals.push(request.proposals[0].clone()),
            3 => request.proposals.push(CheckpointProposal::Forbidden {
                boundary: forbidden_boundary(forbidden),
            }),
            4 => request.generation = CheckpointGenerationDecision::ScopedRefresh {
                changed_domains: BTreeSet::new(),
                manifest_digest: digest(seed.wrapping_add(2)),
            },
            5 => request.deferred_signoff_exception = Some(exception(false)),
            6 => request.deferred_signoff_exception = Some(DeferredSignoffException {
                contract_gap: String::new(),
                ..exception(true)
            }),
            7 => request.proposals = vec![CheckpointProposal::Action {
                action: CheckpointAction::Format { packages: BTreeSet::new() },
            }],
            8 => request.generation = CheckpointGenerationDecision::ScopedRefresh {
                changed_domains: BTreeSet::from([RegenerationClass::Authoring]),
                manifest_digest: digest(seed.wrapping_add(2)),
            },
            9 => request.deferred_signoff_exception = Some(exception(true)),
            _ => unreachable!(),
        }

        let plan = plan_checkpoint(request);
        let should_plan = matches!(mutation, 0 | 8 | 9);
        prop_assert_eq!(plan.is_ok(), should_plan);
        let Ok(plan) = plan else {
            return Ok(());
        };

        let mut observations = plan
            .actions
            .iter()
            .cloned()
            .enumerate()
            .map(|(index, action)| CheckpointActionObservation {
                action,
                outcome: CheckpointActionOutcome::Passed,
                elapsed_millis: u64::try_from(index).unwrap_or_default() + 1,
            })
            .collect::<Vec<_>>();
        let mut forbidden_events = Vec::new();
        let record_mutation = seed % 5;
        match record_mutation {
            0 => {}
            1 => forbidden_events.push(forbidden_boundary(forbidden)),
            2 => {
                observations.pop();
            }
            3 => observations.push(observations[0].clone()),
            4 => observations[0].elapsed_millis = 0,
            _ => unreachable!(),
        }
        let record = record_checkpoint(
            &plan,
            CheckpointObservation {
                implementation_digest: plan.implementation_digest.clone(),
                actions: observations,
                forbidden_events,
            },
        );
        prop_assert_eq!(record.is_ok(), record_mutation == 0);
        if let Ok(record) = record {
            prop_assert_eq!(record.actions.len(), plan.actions.len());
            prop_assert!(record.actions.iter().all(|item| item.elapsed_millis > 0));
        }
    }
}

fn forbidden_boundary(value: u8) -> ForbiddenCheckpointBoundary {
    match value % 6 {
        0 => ForbiddenCheckpointBoundary::Engine,
        1 => ForbiddenCheckpointBoundary::Dagger,
        2 => ForbiddenCheckpointBoundary::NetworkGraph,
        3 => ForbiddenCheckpointBoundary::OtherSdk,
        4 => ForbiddenCheckpointBoundary::UnscopedGeneration,
        _ => ForbiddenCheckpointBoundary::Distribution,
    }
}

fn exception(approved: bool) -> DeferredSignoffException {
    DeferredSignoffException {
        contract_gap: "engine TypeDef registration response".to_owned(),
        model_insufficiency: "the direct sink cannot issue an engine registration query".to_owned(),
        proposed_case: "registration".to_owned(),
        approved,
    }
}
