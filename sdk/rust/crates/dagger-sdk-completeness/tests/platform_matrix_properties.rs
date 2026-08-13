//! Platform admission properties over descriptor, identity, and native-evidence mutations.

use dagger_sdk_completeness::*;
use proptest::prelude::*;

fn target() -> TargetDigest {
    TargetDigest::new(Digest::sha256("platform target"))
}

fn platforms() -> Vec<PlatformDescriptor> {
    [
        OperatingSystem::Linux,
        OperatingSystem::Macos,
        OperatingSystem::Windows,
    ]
    .into_iter()
    .flat_map(|operating_system| {
        [Architecture::Amd64, Architecture::Arm64]
            .into_iter()
            .map(move |architecture| PlatformDescriptor {
                operating_system: operating_system.clone(),
                architecture,
            })
    })
    .collect()
}

fn native_observation(operating_system: OperatingSystem) -> NativePlatformObservation {
    let architecture = match operating_system {
        OperatingSystem::Linux | OperatingSystem::Windows => Architecture::Amd64,
        OperatingSystem::Macos => Architecture::Arm64,
    };
    let link_mechanism = match operating_system {
        OperatingSystem::Linux | OperatingSystem::Macos => NativeLinkMechanism::PosixSymlink,
        OperatingSystem::Windows => NativeLinkMechanism::WindowsReparseOrAcl,
    };
    NativePlatformObservation {
        format_version: ConformanceFormatVersion::V1,
        platform: PlatformDescriptor {
            operating_system,
            architecture,
        },
        runner_digest: Digest::sha256("runner"),
        toolchain_digest: Digest::sha256("rust toolchain"),
        rust_version: SemverVersion::new("1.97.1").unwrap(),
        source_digest: Digest::sha256("source"),
        lockfiles_digest: Digest::sha256("lockfiles"),
        test_digest: Digest::sha256("native suite"),
        link_mechanism,
        domains: CanonicalSet::new(required_native_platform_domains()),
        outcome: NativeJobOutcome::Passed,
        native_execution: true,
        dagger_invocations: 0,
        engine_starts: 0,
        docker_invocations: 0,
        other_sdk_invocations: 0,
    }
}

fn valid_input() -> PortablePlatformMatrixInput {
    PortablePlatformMatrixInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest: target(),
        native_observations: [
            OperatingSystem::Linux,
            OperatingSystem::Macos,
            OperatingSystem::Windows,
        ]
        .into_iter()
        .map(native_observation)
        .collect(),
        descriptors: release_descriptor_matrix(&SemverVersion::new("1.0.0-beta.10").unwrap())
            .into_inner(),
    }
}

fn model_accepts(input: &PortablePlatformMatrixInput) -> bool {
    let expected_platforms = platforms()
        .into_iter()
        .collect::<std::collections::BTreeSet<_>>();
    let actual_platforms = input
        .descriptors
        .iter()
        .map(|descriptor| descriptor.platform.clone())
        .collect::<std::collections::BTreeSet<_>>();
    let expected_os = [
        OperatingSystem::Linux,
        OperatingSystem::Macos,
        OperatingSystem::Windows,
    ]
    .into_iter()
    .collect::<std::collections::BTreeSet<_>>();
    let actual_os = input
        .native_observations
        .iter()
        .map(|observation| observation.platform.operating_system.clone())
        .collect::<std::collections::BTreeSet<_>>();
    let shared = input.native_observations.first().map(|observation| {
        (
            &observation.source_digest,
            &observation.lockfiles_digest,
            &observation.test_digest,
        )
    });
    input.descriptors.len() == 6
        && actual_platforms == expected_platforms
        && input.descriptors.iter().all(|descriptor| {
            descriptor
                == &release_archive_descriptor(
                    &SemverVersion::new("1.0.0-beta.10").unwrap(),
                    descriptor.platform.clone(),
                )
        })
        && input.native_observations.len() == 3
        && actual_os == expected_os
        && input.native_observations.iter().all(|observation| {
            observation.rust_version == SemverVersion::new("1.97.1").unwrap()
                && observation.domains.len() == required_native_platform_domains().len()
                && observation
                    .domains
                    .iter()
                    .all(|domain| required_native_platform_domains().contains(domain))
                && observation.outcome == NativeJobOutcome::Passed
                && observation.native_execution
                && observation.dagger_invocations == 0
                && observation.engine_starts == 0
                && observation.docker_invocations == 0
                && observation.other_sdk_invocations == 0
                && shared.is_some_and(|identity| {
                    identity
                        == (
                            &observation.source_digest,
                            &observation.lockfiles_digest,
                            &observation.test_digest,
                        )
                })
        })
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(100))]

    #[test]
    fn property_16_descriptor_and_exact_engine_platform_claims_never_widen(
        mutation in 0_u8..8,
        index in any::<usize>(),
    ) {
        let mut input = valid_input();
        match mutation {
            0 => input.descriptors.reverse(),
            1 => {
                let position = index % input.descriptors.len();
                input.descriptors.remove(position);
            }
            2 => {
                let position = index % input.descriptors.len();
                input.descriptors.push(input.descriptors[position].clone());
            }
            3 => input.descriptors[0].archive_name.push_str(".wrong"),
            4 => input.descriptors[0].executable_member = "dagger.exe".to_owned(),
            5 => input.descriptors[0].archive_format = ReleaseArchiveFormat::Zip,
            6 => input.native_observations[0].source_digest = Digest::sha256("other source"),
            7 => input.native_observations[0].test_digest = Digest::sha256("other suite"),
            _ => unreachable!(),
        }
        prop_assert_eq!(assemble_portable_platform_matrix(input.clone()).is_ok(), model_accepts(&input));

        let claim = ExactEnginePlatformClaim {
            artifact_platform: if mutation < 4 {
                PlatformDescriptor::linux_amd64()
            } else {
                platforms()[index % 6].clone()
            },
            verdict_platform: if mutation == 5 {
                platforms()[(index + 1) % 6].clone()
            } else if mutation < 4 {
                PlatformDescriptor::linux_amd64()
            } else {
                platforms()[index % 6].clone()
            },
            initial_signoff: mutation < 4,
        };
        let expected_claim = claim.artifact_platform == claim.verdict_platform
            && (!claim.initial_signoff
                || claim.artifact_platform == PlatformDescriptor::linux_amd64());
        prop_assert_eq!(admit_exact_engine_platform_claim(&claim).is_ok(), expected_claim);
    }

    #[test]
    fn property_17_native_os_closure_engine_free(
        mutation in 0_u8..12,
        index in any::<usize>(),
    ) {
        let mut input = valid_input();
        match mutation {
            0 => input.native_observations.reverse(),
            1 => { input.native_observations.remove(index % 3); }
            2 => input.native_observations.push(input.native_observations[index % 3].clone()),
            3 => input.native_observations[index % 3].outcome = NativeJobOutcome::Failed,
            4 => input.native_observations[index % 3].outcome = NativeJobOutcome::Skipped,
            5 => input.native_observations[index % 3].native_execution = false,
            6 => input.native_observations[index % 3].engine_starts = 1,
            7 => input.native_observations[index % 3].other_sdk_invocations = 1,
            8 => input.native_observations[index % 3].dagger_invocations = 1,
            9 => input.native_observations[index % 3].docker_invocations = 1,
            10 => {
                input.native_observations[index % 3].domains = CanonicalSet::new([
                    NativePlatformDomain::ExecutableDiscovery,
                ]);
            }
            11 => input.native_observations[index % 3].rust_version = SemverVersion::new("1.96.0").unwrap(),
            _ => unreachable!(),
        }
        prop_assert_eq!(assemble_portable_platform_matrix(input.clone()).is_ok(), model_accepts(&input));
    }
}
