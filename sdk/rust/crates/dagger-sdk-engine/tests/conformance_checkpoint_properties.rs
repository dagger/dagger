//! Closed action, change-trigger, timing, and evidence-accounting properties.

use std::collections::BTreeSet;

use dagger_sdk_engine::*;
use proptest::prelude::*;

fn digest(seed: u16) -> Sha256Digest {
    Sha256Digest::new(format!("sha256:{seed:04x}{}", "00".repeat(30))).unwrap()
}

fn expected_cargo(action: &Feature8CheckpointAction) -> u32 {
    match action {
        Feature8CheckpointAction::DirectGoSignoffAdapter
        | Feature8CheckpointAction::CleanOutput => 0,
        _ => 1,
    }
}

fn valid_request(seed: u16, reuse_mask: u16) -> Feature8CheckpointRequest {
    let actions = feature8_checkpoint_actions();
    let inputs = actions
        .iter()
        .enumerate()
        .map(|(index, action)| {
            let owning = digest(seed.wrapping_add(index as u16));
            let prior = (index > 0 && reuse_mask & (1 << (index % 16)) != 0).then(|| {
                Feature8PriorActionObservation {
                    owning_input_digest: owning.clone(),
                    outcome: Feature8ActionOutcome::Passed,
                    elapsed_millis: 100 + index as u64,
                    cargo_invocations: expected_cargo(action),
                    output_digest: digest(seed.wrapping_add(100 + index as u16)),
                }
            });
            Feature8ActionInput {
                action: action.clone(),
                owning_input_digest: owning,
                expected_cargo_invocations: expected_cargo(action),
                prior,
            }
        })
        .collect();
    Feature8CheckpointRequest {
        implementation_digest: digest(seed.wrapping_add(500)),
        proposals: actions
            .iter()
            .cloned()
            .map(|action| Feature8CheckpointProposal::Action { action })
            .collect(),
        inputs,
        phase_budgets: feature8_checkpoint_phase_budgets(),
        generation: CheckpointGenerationDecision::ReuseChecked {
            manifest_digest: digest(seed.wrapping_add(600)),
        },
    }
}

#[test]
fn complete_action_inventory_accounts_for_every_property_and_no_engine_boundary() {
    let actions = feature8_checkpoint_actions();
    let properties = actions
        .iter()
        .filter_map(|action| match action {
            Feature8CheckpointAction::NamedTests { properties, .. } => Some(properties),
            _ => None,
        })
        .flatten()
        .map(|property| property.get())
        .collect::<BTreeSet<_>>();
    assert_eq!(properties, BTreeSet::from_iter(1..=24));
    assert!(actions.contains(&Feature8CheckpointAction::CargoDeny));
    assert!(actions.contains(&Feature8CheckpointAction::DirectGoSignoffAdapter));
    assert!(actions.contains(&Feature8CheckpointAction::NativeEvidenceAggregation));
    assert!(actions.contains(&Feature8CheckpointAction::EvidenceAggregation));
    assert!(actions.contains(&Feature8CheckpointAction::CheckedAssetDrift));
    assert!(actions.contains(&Feature8CheckpointAction::CleanOutput));
    assert!(actions.iter().all(|action| match action {
        Feature8CheckpointAction::Format { packages }
        | Feature8CheckpointAction::SourcePolicy { packages }
        | Feature8CheckpointAction::Clippy { packages }
        | Feature8CheckpointAction::Rustdoc { packages } => packages.iter().all(|package| {
            matches!(
                package,
                CheckpointPackage::DaggerSdkEngine | CheckpointPackage::DaggerSdkCompleteness
            )
        }),
        Feature8CheckpointAction::Check { package }
        | Feature8CheckpointAction::NamedTests { package, .. } => matches!(
            package,
            CheckpointPackage::DaggerSdkEngine | CheckpointPackage::DaggerSdkCompleteness
        ),
        _ => true,
    }));
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // The typed inventory cannot express an engine, Dagger, module, target, network, or shell action.
    #[test]
    fn property_21_checkpoints_engine_free_by_construction(
        seed in any::<u16>(),
        mutation in 0_u8..11,
        forbidden in 0_u8..10,
    ) {
        let mut request = valid_request(seed, 0);
        match mutation {
            0 => {}
            1 => request.proposals.push(Feature8CheckpointProposal::Forbidden {
                boundary: match forbidden {
                    0 => ForbiddenCheckpointBoundary::Engine,
                    1 => ForbiddenCheckpointBoundary::Dagger,
                    2 => ForbiddenCheckpointBoundary::Module,
                    3 => ForbiddenCheckpointBoundary::NetworkGraph,
                    4 => ForbiddenCheckpointBoundary::OtherSdk,
                    5 => ForbiddenCheckpointBoundary::UnscopedGeneration,
                    6 => ForbiddenCheckpointBoundary::Distribution,
                    7 => ForbiddenCheckpointBoundary::TargetArtifact,
                    8 => ForbiddenCheckpointBoundary::TargetArtifactScan,
                    _ => ForbiddenCheckpointBoundary::ArbitraryShell,
                },
            }),
            2 => { request.proposals.pop(); }
            3 => request.proposals.push(request.proposals[0].clone()),
            4 => request.proposals[0] = Feature8CheckpointProposal::Action {
                action: Feature8CheckpointAction::Check {
                    package: CheckpointPackage::DaggerSdk,
                },
            },
            5 => { request.inputs.pop(); }
            6 => request.inputs.push(request.inputs[0].clone()),
            7 => request.inputs[0].expected_cargo_invocations += 1,
            8 => { request.phase_budgets.pop(); }
            9 => request.phase_budgets[0].maximum_millis = 0,
            10 => request.generation = CheckpointGenerationDecision::ScopedRefresh {
                changed_domains: BTreeSet::new(),
                manifest_digest: digest(seed.wrapping_add(700)),
            },
            _ => unreachable!(),
        }
        prop_assert_eq!(plan_feature8_checkpoint(request).is_ok(), mutation == 0);
    }

    // Current evidence is admitted only when every action is exact, bounded, counted, and current.
    #[test]
    fn property_22_checkpoint_evidence_timed_counted_reusable_complete(
        seed in any::<u16>(),
        reuse_mask in any::<u16>(),
        mutation in 0_u8..12,
        index in any::<usize>(),
    ) {
        let plan = plan_feature8_checkpoint(valid_request(seed, reuse_mask)).unwrap();
        let mut actions = plan
            .actions
            .iter()
            .enumerate()
            .filter(|(_, action)| action.disposition == Feature8ActionDisposition::Execute)
            .map(|(position, action)| Feature8ActionObservation {
                action: action.action.clone(),
                owning_input_digest: action.owning_input_digest.clone(),
                outcome: Feature8ActionOutcome::Passed,
                elapsed_millis: 100 + position as u64,
                cargo_invocations: action.expected_cargo_invocations,
                output_digest: digest(seed.wrapping_add(800 + position as u16)),
            })
            .collect::<Vec<_>>();
        let selected = index % actions.len();
        let mut observation = Feature8CheckpointObservation {
            implementation_digest: plan.implementation_digest.clone(),
            actions: actions.clone(),
            forbidden_events: Vec::new(),
            sdk_signoff_claimed: false,
        };
        match mutation {
            0 => {}
            1 => { observation.actions.pop(); }
            2 => observation.actions.push(actions.remove(selected)),
            3 => observation.implementation_digest = digest(seed.wrapping_add(900)),
            4 => observation.forbidden_events.push(ForbiddenCheckpointBoundary::Engine),
            5 => observation.sdk_signoff_claimed = true,
            6 => observation.actions[selected].owning_input_digest = digest(seed.wrapping_add(901)),
            7 => observation.actions[selected].cargo_invocations += 1,
            8 => observation.actions[selected].elapsed_millis = 0,
            9 => observation.actions[selected].outcome = Feature8ActionOutcome::Failed,
            10 => {
                observation.actions[selected].outcome = Feature8ActionOutcome::TimedOut;
                observation.actions[selected].elapsed_millis = 900_000;
            }
            11 => observation.actions[selected].elapsed_millis = 900_001,
            _ => unreachable!(),
        }
        let record = record_feature8_checkpoint(&plan, observation);
        let structurally_valid = matches!(mutation, 0 | 9 | 10);
        prop_assert_eq!(record.is_ok(), structurally_valid);
        if let Ok(record) = record {
            let closure = admit_feature8_checkpoint_closure(&record);
            prop_assert_eq!(closure.is_ok(), mutation == 0);
            prop_assert_eq!(record.complete, mutation == 0);
            prop_assert!(!record.sdk_signoff_claimed);
        }
    }
}
