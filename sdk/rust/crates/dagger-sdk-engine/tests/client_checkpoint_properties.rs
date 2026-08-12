//! Change-triggered, engine-free standalone-client checkpoint properties.

use std::collections::BTreeSet;

use dagger_codegen::module::RegenerationClass;
use dagger_sdk_engine::{
    CheckpointAction, CheckpointActionObservation, CheckpointActionOutcome,
    CheckpointGenerationDecision, CheckpointPackage, CheckpointProposal, CheckpointRequest,
    CheckpointTestTarget, ClientCargoExpectation, ClientCheckedAssetState,
    ClientCheckpointActionObservation, ClientCheckpointObservation, ClientCheckpointRequest,
    DeferredSignoffException, ForbiddenCheckpointBoundary, RustGoAbiPackage, Sha256Digest,
    plan_client_checkpoint, record_client_checkpoint,
};
use proptest::prelude::*;

fn digest(seed: u16) -> Sha256Digest {
    Sha256Digest::new(format!("sha256:{seed:04x}{}", "00".repeat(30))).unwrap()
}

fn test_action(package: CheckpointPackage, target: &str) -> CheckpointAction {
    CheckpointAction::Test {
        package,
        targets: BTreeSet::from([CheckpointTestTarget::new(target).unwrap()]),
        properties: BTreeSet::new(),
    }
}

fn valid_request(seed: u16, refresh: bool) -> ClientCheckpointRequest {
    let engine = test_action(
        CheckpointPackage::DaggerSdkEngine,
        "client_usability_properties",
    );
    let sdk = test_action(
        CheckpointPackage::DaggerSdk,
        "generated_client_query_properties",
    );
    let go = CheckpointAction::DirectGoAbi {
        package: RustGoAbiPackage::Runtime,
    };
    let owning = digest(seed);
    let checked_input = if refresh {
        digest(seed.wrapping_add(1))
    } else {
        owning.clone()
    };
    let output = digest(seed.wrapping_add(2));
    ClientCheckpointRequest {
        checkpoint: CheckpointRequest {
            implementation_digest: digest(seed.wrapping_add(3)),
            proposals: [&engine, &sdk, &go]
                .into_iter()
                .cloned()
                .map(|action| CheckpointProposal::Action { action })
                .collect(),
            generation: if refresh {
                CheckpointGenerationDecision::ScopedRefresh {
                    changed_domains: BTreeSet::from([RegenerationClass::Generator]),
                    manifest_digest: output.clone(),
                }
            } else {
                CheckpointGenerationDecision::ReuseChecked {
                    manifest_digest: output.clone(),
                }
            },
            deferred_signoff_exception: Some(DeferredSignoffException {
                contract_gap: "engine-owned initialized client record".to_owned(),
                model_insufficiency: "direct fixtures cannot create an engine record".to_owned(),
                proposed_case: "one initialized local client".to_owned(),
                approved: true,
            }),
        },
        asset: ClientCheckedAssetState {
            owning_input_digest: owning,
            checked_input_digest: checked_input,
            checked_output_digest: output,
        },
        cargo: vec![
            ClientCargoExpectation {
                action: engine,
                invocations: 6,
            },
            ClientCargoExpectation {
                action: sdk,
                invocations: 1,
            },
            ClientCargoExpectation {
                action: go,
                invocations: 0,
            },
        ],
    }
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // A client checkpoint is admitted only with exact scope, change decision, timings, and counts.
    #[test]
    fn property_25_local_checkpoints_engine_free_change_triggered(
        seed in any::<u16>(),
        refresh in any::<bool>(),
        plan_mutation in 0_u8..7,
        record_mutation in 0_u8..7,
    ) {
        let mut request = valid_request(seed, refresh);
        match plan_mutation {
            0 => {}
            1 => request.checkpoint.proposals.push(CheckpointProposal::Forbidden {
                boundary: ForbiddenCheckpointBoundary::Engine,
            }),
            2 => request.checkpoint.proposals.push(request.checkpoint.proposals[0].clone()),
            3 => {
                request.cargo.pop();
            }
            4 => request.cargo[0].invocations = 0,
            5 => request.asset.checked_input_digest = if refresh {
                request.asset.owning_input_digest.clone()
            } else {
                digest(seed.wrapping_add(9))
            },
            6 => request.checkpoint.proposals[0] = CheckpointProposal::Action {
                action: CheckpointAction::Check {
                    package: CheckpointPackage::DaggerBootstrap,
                    all_features: true,
                },
            },
            _ => unreachable!(),
        }
        let plan = plan_client_checkpoint(request);
        prop_assert_eq!(plan.is_ok(), plan_mutation == 0);
        let Ok(plan) = plan else { return Ok(()); };

        let mut actions = plan
            .cargo
            .iter()
            .enumerate()
            .map(|(index, expected)| ClientCheckpointActionObservation {
                action: CheckpointActionObservation {
                    action: expected.action.clone(),
                    outcome: CheckpointActionOutcome::Passed,
                    elapsed_millis: u64::try_from(index).unwrap() + 1,
                },
                cargo_invocations: expected.invocations,
            })
            .collect::<Vec<_>>();
        let mut observation = ClientCheckpointObservation {
            implementation_digest: plan.checkpoint.implementation_digest.clone(),
            asset_input_digest: plan.asset.owning_input_digest.clone(),
            asset_output_digest: plan.asset.checked_output_digest.clone(),
            actions: actions.clone(),
            forbidden_events: Vec::new(),
        };
        match record_mutation {
            0 => {}
            1 => observation.forbidden_events.push(ForbiddenCheckpointBoundary::Dagger),
            2 => { observation.actions.pop(); }
            3 => observation.actions.push(actions.remove(0)),
            4 => observation.actions[0].action.elapsed_millis = 0,
            5 => observation.actions[0].cargo_invocations += 1,
            6 => observation.asset_output_digest = digest(seed.wrapping_add(11)),
            _ => unreachable!(),
        }
        let record = record_client_checkpoint(&plan, observation);
        prop_assert_eq!(record.is_ok(), record_mutation == 0);
        if let Ok(record) = record {
            prop_assert_eq!(record.cargo, plan.cargo);
            prop_assert_eq!(record.checkpoint.actions.len(), plan.checkpoint.actions.len());
        }
    }
}
