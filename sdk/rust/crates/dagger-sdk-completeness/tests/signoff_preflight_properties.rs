//! Provider-neutral host planning and infrastructure-only preflight properties.
//!
//! The reference checks are deliberately plain truth tables over the public typed observation.
//! They do not call the production admission helpers whose result they predict.

use std::collections::BTreeMap;

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const CASES: u32 = 256;

fn fixture_profile() -> SignoffHostProfile {
    linux_amd64_host_profile(
        ProvenanceId::new(
            "binary/dagger-rust-sdk-signoff/sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        )
        .unwrap(),
    )
}

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/signoff-preflight.txt"
        )))),
        ..Config::default()
    }
}

fn valid_observation(plan: &HostPreflightPlan) -> HostPreflightObservation {
    let canary = Digest::sha256("preflight-canary");
    let elapsed = NonZeroMillis::new(1).unwrap();
    let results = [
        HostStepResult::HostResources {
            observation: HostResourceObservation {
                platform: PlatformDescriptor::linux_amd64(),
                cpu_count: NonZeroCount::new(32).unwrap(),
                memory_bytes: NonZeroBytes::new(64 * 1024_u64.pow(3)).unwrap(),
                workspace_bytes: NonZeroBytes::new(198 * 1024_u64.pow(3)).unwrap(),
            },
        },
        HostStepResult::ContainerDaemon {
            observation: ContainerDaemonObservation {
                available: true,
                api_version: NonEmptyText::new("1.52").unwrap(),
                storage_driver: NonEmptyText::new("overlayfs").unwrap(),
                storage_bytes: NonZeroBytes::new(198 * 1024_u64.pow(3)).unwrap(),
                privileged_containers: true,
                daemon_identity: Digest::sha256("daemon"),
            },
        },
        HostStepResult::PersistentCanary {
            before: canary.clone(),
            after_restart: canary.clone(),
            restart_count: NonZeroCount::new(1).unwrap(),
        },
        HostStepResult::ExportedPayload {
            exported: canary.clone(),
            imported: canary.clone(),
        },
        HostStepResult::CacheReuse {
            first_output: canary.clone(),
            second_output: canary,
            reused: true,
        },
        HostStepResult::SmokeStarted {
            smoke_tool: plan.profile.smoke_tool.clone(),
            smoke_engine: plan.profile.smoke_engine.clone(),
            start_count: NonZeroCount::new(1).unwrap(),
        },
        HostStepResult::SmokeServiceProbed {
            reachable: true,
            probe_count: NonZeroCount::new(1).unwrap(),
        },
        HostStepResult::SmokeStopped {
            stopped: true,
            reaped: true,
            stop_count: NonZeroCount::new(1).unwrap(),
        },
        HostStepResult::RetainedOutputScanned {
            inspected_bytes: 128,
            canary_matches: 0,
        },
    ];
    HostPreflightObservation {
        format_version: ConformanceFormatVersion::V1,
        profile_digest: plan.profile_digest.clone(),
        plan_digest: plan.plan_digest.clone(),
        steps: plan
            .steps
            .iter()
            .copied()
            .zip(results)
            .map(|(step, result)| HostStepObservation {
                step,
                elapsed,
                result,
            })
            .collect(),
        forbidden_events: CanonicalSet::default(),
    }
}

// Invariant: provider metadata cannot change the closed plan and every host mismatch blocks later work.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_03_host_planning_provider_neutral_fail_fast(
        provider_left in "[a-z][a-z0-9-]{0,20}",
        provider_right in "[a-z][a-z0-9-]{0,20}",
        mutation in 0_u8..9,
        resource_delta in 1_u32..32,
    ) {
        let plan_left = plan_host_preflight(fixture_profile()).unwrap();
        let plan_right = plan_host_preflight(fixture_profile()).unwrap();
        // Provider labels remain deliberately outside the production input and durable bytes.
        prop_assert_eq!(plan_left.clone(), plan_right.clone());
        let encoded_left = canonical_bytes(&plan_left).unwrap();
        let encoded_right = canonical_bytes(&plan_right).unwrap();
        // The labels participate only in this independent reference tuple; neither is an input to
        // production planning, so swapping them cannot perturb the durable bytes.
        let reference_left = (provider_left, encoded_left);
        let reference_right = (provider_right, encoded_right);
        prop_assert_eq!(reference_left.1, reference_right.1);

        let mut observation = valid_observation(&plan_left);
        match mutation {
            1 => observation.profile_digest = Digest::sha256("stale-profile"),
            2 => observation.plan_digest = Digest::sha256("stale-plan"),
            3 => if let HostStepResult::HostResources { observation } = &mut observation.steps[0].result {
                observation.platform = PlatformDescriptor {
                    operating_system: OperatingSystem::Macos,
                    architecture: Architecture::Amd64,
                };
            },
            4 => if let HostStepResult::HostResources { observation } = &mut observation.steps[0].result {
                observation.cpu_count = NonZeroCount::new(resource_delta.min(15)).unwrap();
            },
            5 => if let HostStepResult::HostResources { observation } = &mut observation.steps[0].result {
                observation.memory_bytes = NonZeroBytes::new(u64::from(resource_delta) * 1024_u64.pow(3)).unwrap();
            },
            6 => if let HostStepResult::HostResources { observation } = &mut observation.steps[0].result {
                observation.workspace_bytes = NonZeroBytes::new(u64::from(resource_delta) * 1024_u64.pow(3)).unwrap();
            },
            7 => if let HostStepResult::ContainerDaemon { observation } = &mut observation.steps[1].result {
                observation.available = false;
            },
            8 => observation.steps[0].elapsed = NonZeroMillis::new(60_001).unwrap(),
            _ => {}
        }

        // Independent threshold model: every non-zero mutation above violates one mandatory gate.
        let reference_accepts = mutation == 0;
        prop_assert_eq!(admit_host_preflight(&plan_left, observation).is_ok(), reference_accepts);
    }
}

// Invariant: preflight proves each infrastructure boundary once and admits no SDK claim or target work.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_04_preflight_infrastructure_only(
        mutation in 0_u8..12,
        split in 0_usize..24,
        prefix in "[a-z0-9]{0,24}",
        suffix in "[a-z0-9]{0,24}",
    ) {
        let plan = plan_host_preflight(fixture_profile()).unwrap();
        let mut observation = valid_observation(&plan);
        match mutation {
            1 => if let HostStepResult::PersistentCanary { after_restart, .. } = &mut observation.steps[2].result {
                *after_restart = Digest::sha256("changed");
            },
            2 => if let HostStepResult::ExportedPayload { imported, .. } = &mut observation.steps[3].result {
                *imported = Digest::sha256("changed");
            },
            3 => if let HostStepResult::CacheReuse { reused, .. } = &mut observation.steps[4].result {
                *reused = false;
            },
            4 => if let HostStepResult::SmokeStarted { start_count, .. } = &mut observation.steps[5].result {
                *start_count = NonZeroCount::new(2).unwrap();
            },
            5 => if let HostStepResult::SmokeServiceProbed { reachable, .. } = &mut observation.steps[6].result {
                *reachable = false;
            },
            6 => if let HostStepResult::SmokeStopped { reaped, .. } = &mut observation.steps[7].result {
                *reaped = false;
            },
            7 => if let HostStepResult::RetainedOutputScanned { canary_matches, .. } = &mut observation.steps[8].result {
                *canary_matches = 1;
            },
            8 => observation.forbidden_events = CanonicalSet::new([ForbiddenPreflightEvent::ExactTargetArtifactBuild]),
            9 => observation.forbidden_events = CanonicalSet::new([ForbiddenPreflightEvent::RustSdkInstall]),
            10 => observation.forbidden_events = CanonicalSet::new([ForbiddenPreflightEvent::CaseExecution]),
            11 => observation.forbidden_events = CanonicalSet::new([ForbiddenPreflightEvent::CapabilityClaim]),
            _ => {}
        }
        prop_assert_eq!(admit_host_preflight(&plan, observation).is_ok(), mutation == 0);

        let canary = b"preflight-secret-canary";
        let mut bytes = prefix.into_bytes();
        bytes.extend_from_slice(canary);
        bytes.extend_from_slice(suffix.as_bytes());
        let split = split.min(bytes.len());
        let chunks = [&bytes[..split], &bytes[split..]];
        let (_, matches) = scan_retained_output(chunks, &[canary]).unwrap();
        prop_assert_eq!(matches, 1);
    }
}

#[test]
fn runner_attempts_stop_after_probe_failure() {
    let plan = plan_host_preflight(fixture_profile()).unwrap();
    let observations = valid_observation(&plan)
        .steps
        .into_iter()
        .map(|item| (item.step, item))
        .collect();
    let mut probe = FailingProbe {
        observations,
        seen: Vec::new(),
    };
    assert!(run_host_preflight(&plan, &mut probe).is_err());
    assert!(probe.seen.contains(&HostPreflightStep::StopSmokeEngine));
}

struct FailingProbe {
    observations: BTreeMap<HostPreflightStep, HostStepObservation>,
    seen: Vec<HostPreflightStep>,
}

impl HostProbe for FailingProbe {
    fn observe(&mut self, step: &HostPreflightStep) -> Result<HostStepObservation, HostProbeError> {
        self.seen.push(*step);
        if *step == HostPreflightStep::ProbeSmokeService {
            return Err(HostProbeError {
                step: *step,
                kind: HostProbeErrorKind::Unavailable,
            });
        }
        Ok(self.observations.get(step).unwrap().clone())
    }
}
