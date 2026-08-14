//! Native operating-system evidence and pure release-descriptor closure.
//!
//! Descriptor simulation proves only archive selection. Native observations are admitted through
//! a separate exact-identity boundary so a pure Linux model can never stand in for macOS or
//! Windows process, path, permission, and cleanup behaviour.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    Architecture, CanonicalSet, Digest, OperatingSystem, SemverVersion, TargetDigest,
};

use super::{
    ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, PlatformDescriptor,
};

/// Archive representation selected by one release platform.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ReleaseArchiveFormat {
    /// Gzip-compressed tar archive used on Linux and macOS.
    TarGz,
    /// ZIP archive used on Windows.
    Zip,
}

/// Pure release descriptor expected from the production SDK for one platform.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReleaseArchiveDescriptor {
    /// Exact OS and architecture pair.
    pub platform: PlatformDescriptor,
    /// Canonical release archive name.
    pub archive_name: String,
    /// Executable member selected from the archive.
    pub executable_member: String,
    /// Platform-specific archive representation.
    pub archive_format: ReleaseArchiveFormat,
}

/// Closed native behaviour domains which must execute on the matching host family.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NativePlatformDomain {
    /// Native PATH lookup and executable discovery.
    ExecutableDiscovery,
    /// Cache publication, retention, and permission handling.
    CachePublication,
    /// Path containment and symlink, reparse, or ACL boundaries.
    PathAndLinkBoundary,
    /// Child startup, termination, and reaping.
    ChildLifecycle,
    /// Isolation of the first control line from diagnostics.
    ControlLineIsolation,
    /// Bounded native diagnostic collection.
    Diagnostics,
    /// Credential redaction across arbitrary chunks.
    Redaction,
}

/// Honest terminal result of a native platform job.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NativeJobOutcome {
    /// Every required native domain passed.
    Passed,
    /// At least one required native domain failed.
    Failed,
    /// The job did not execute its required native domains.
    Skipped,
}

/// Native path/link mechanism exercised by one OS job.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NativeLinkMechanism {
    /// POSIX symlink and executable-mode behaviour.
    PosixSymlink,
    /// Windows reparse-point or equivalent ACL/path behaviour.
    WindowsReparseOrAcl,
}

/// One bounded observation produced only after the fixed Rust SDK native suite passes.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct NativePlatformObservation {
    /// Durable observation format.
    pub format_version: ConformanceFormatVersion,
    /// Actual host platform; its architecture is observed rather than inferred.
    pub platform: PlatformDescriptor,
    /// Exact runner image or environment identity, retained only as a digest.
    pub runner_digest: Digest,
    /// Exact Rust toolchain identity.
    pub toolchain_digest: Digest,
    /// Exact Rust version used by the job.
    pub rust_version: SemverVersion,
    /// Digest of the tested Rust source closure.
    pub source_digest: Digest,
    /// Digest of every committed lockfile used by the native suite.
    pub lockfiles_digest: Digest,
    /// Digest of the closed command and test inventory.
    pub test_digest: Digest,
    /// Native mechanism used for the OS-specific path/link boundary.
    pub link_mechanism: NativeLinkMechanism,
    /// Exact required native domains reported by the suite.
    pub domains: CanonicalSet<NativePlatformDomain>,
    /// Honest terminal job outcome.
    pub outcome: NativeJobOutcome,
    /// True only for direct execution on the recorded operating system.
    pub native_execution: bool,
    /// Number of Dagger CLI invocations observed by the fixed runner.
    pub dagger_invocations: u32,
    /// Number of Dagger engine starts observed by the fixed runner.
    pub engine_starts: u32,
    /// Number of Docker invocations observed by the fixed runner.
    pub docker_invocations: u32,
    /// Number of other SDK build or test invocations observed by the fixed runner.
    pub other_sdk_invocations: u32,
}

/// Authored matrix input; vectors retain duplicates so admission can reject them.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PortablePlatformMatrixInput {
    /// Durable matrix format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target associated with the implementation closure.
    pub target_digest: TargetDigest,
    /// Three native job observations.
    pub native_observations: Vec<NativePlatformObservation>,
    /// Six pure archive descriptors.
    pub descriptors: Vec<ReleaseArchiveDescriptor>,
}

/// Admitted three-OS native evidence plus the complete pure descriptor cross-product.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PortablePlatformMatrix {
    /// Durable matrix format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target associated with the implementation closure.
    pub target_digest: TargetDigest,
    /// Native observations indexed by their actual OS.
    pub native_observations: BTreeMap<OperatingSystem, NativePlatformObservation>,
    /// Canonical complete pure descriptor set.
    pub descriptors: CanonicalSet<ReleaseArchiveDescriptor>,
    /// Domain-separated identity of the complete matrix.
    pub matrix_digest: Digest,
}

/// Routine Linux/macOS observation set which explicitly is not a portable matrix.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DevelopmentNativePlatformSet {
    /// Durable observation-set format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target associated with the candidate implementation.
    pub target_digest: TargetDigest,
    /// Current Linux and macOS observations, indexed by real native OS.
    pub native_observations: BTreeMap<OperatingSystem, NativePlatformObservation>,
    /// Domain-separated identity of this deliberately non-portable set.
    pub observation_set_digest: Digest,
}

/// Exact-engine platform claim evaluated separately from portable native closure.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExactEnginePlatformClaim {
    /// Platform of the reusable exact-target artifact.
    pub artifact_platform: PlatformDescriptor,
    /// Platform named by the atomic verdict.
    pub verdict_platform: PlatformDescriptor,
    /// True only for the initial sign-off platform fixed by policy.
    pub initial_signoff: bool,
}

/// Constructs the pure descriptor selected by the production release naming contract.
pub fn release_archive_descriptor(
    cli_version: &SemverVersion,
    platform: PlatformDescriptor,
) -> ReleaseArchiveDescriptor {
    let (os, archive_format, extension, member) = match platform.operating_system {
        OperatingSystem::Linux => ("linux", ReleaseArchiveFormat::TarGz, "tar.gz", "dagger"),
        OperatingSystem::Macos => ("darwin", ReleaseArchiveFormat::TarGz, "tar.gz", "dagger"),
        OperatingSystem::Windows => ("windows", ReleaseArchiveFormat::Zip, "zip", "dagger.exe"),
    };
    let architecture = match platform.architecture {
        Architecture::Amd64 => "amd64",
        Architecture::Arm64 => "arm64",
    };
    ReleaseArchiveDescriptor {
        archive_name: format!("dagger_v{cli_version}_{os}_{architecture}.{extension}"),
        executable_member: member.to_owned(),
        archive_format,
        platform,
    }
}

/// Returns the exact six-platform descriptor suite.
pub fn release_descriptor_matrix(
    cli_version: &SemverVersion,
) -> CanonicalSet<ReleaseArchiveDescriptor> {
    CanonicalSet::new(
        required_platforms()
            .into_iter()
            .map(|platform| release_archive_descriptor(cli_version, platform)),
    )
}

/// Returns every native behaviour domain required from each OS job.
pub fn required_native_platform_domains() -> BTreeSet<NativePlatformDomain> {
    use NativePlatformDomain as Domain;
    [
        Domain::ExecutableDiscovery,
        Domain::CachePublication,
        Domain::PathAndLinkBoundary,
        Domain::ChildLifecycle,
        Domain::ControlLineIsolation,
        Domain::Diagnostics,
        Domain::Redaction,
    ]
    .into_iter()
    .collect()
}

/// Admits exactly three native jobs and all six pure descriptors.
pub fn assemble_portable_platform_matrix(
    input: PortablePlatformMatrixInput,
) -> Result<PortablePlatformMatrix, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    let mut native = BTreeMap::new();
    let expected_domains = required_native_platform_domains();
    let expected_version = SemverVersion::new("1.97.1").expect("checked Rust version is valid");
    let mut shared_identities: Option<(Digest, Digest, Digest)> = None;

    for observation in input.native_observations {
        let expected_link = match observation.platform.operating_system {
            OperatingSystem::Linux | OperatingSystem::Macos => NativeLinkMechanism::PosixSymlink,
            OperatingSystem::Windows => NativeLinkMechanism::WindowsReparseOrAcl,
        };
        let identities = (
            observation.source_digest.clone(),
            observation.lockfiles_digest.clone(),
            observation.test_digest.clone(),
        );
        let identity_matches = shared_identities
            .as_ref()
            .is_none_or(|expected| expected == &identities);
        shared_identities.get_or_insert(identities);
        if observation.rust_version != expected_version
            || observation.domains.iter().copied().collect::<BTreeSet<_>>() != expected_domains
            || observation.outcome != NativeJobOutcome::Passed
            || !observation.native_execution
            || observation.link_mechanism != expected_link
            || observation.dagger_invocations != 0
            || observation.engine_starts != 0
            || observation.docker_invocations != 0
            || observation.other_sdk_invocations != 0
            || !identity_matches
        {
            diagnostics.push(platform_diagnostic(
                ConformanceDiagnosticCode::PlatformMatrixIncomplete,
                "native platform observation is stale failed skipped or non-native",
            ));
        }
        let os = observation.platform.operating_system.clone();
        if native.insert(os, observation).is_some() {
            diagnostics.push(platform_diagnostic(
                ConformanceDiagnosticCode::PlatformMatrixIncomplete,
                "native operating-system observation is duplicated",
            ));
        }
    }
    if native.keys().cloned().collect::<BTreeSet<_>>() != required_operating_systems() {
        diagnostics.push(platform_diagnostic(
            ConformanceDiagnosticCode::PlatformMatrixIncomplete,
            "native operating-system observation set is incomplete",
        ));
    }

    let mut descriptors = BTreeMap::new();
    for descriptor in input.descriptors {
        let expected = release_archive_descriptor(
            &SemverVersion::new("1.0.0-beta.10").expect("checked CLI version is valid"),
            descriptor.platform.clone(),
        );
        if descriptor != expected {
            diagnostics.push(platform_diagnostic(
                ConformanceDiagnosticCode::PlatformMatrixIncomplete,
                "release descriptor does not match the exact platform policy",
            ));
        }
        if descriptors
            .insert(descriptor.platform.clone(), descriptor)
            .is_some()
        {
            diagnostics.push(platform_diagnostic(
                ConformanceDiagnosticCode::PlatformMatrixIncomplete,
                "release descriptor platform is duplicated",
            ));
        }
    }
    if descriptors.keys().cloned().collect::<BTreeSet<_>>() != required_platforms() {
        diagnostics.push(platform_diagnostic(
            ConformanceDiagnosticCode::PlatformMatrixIncomplete,
            "release descriptor matrix is incomplete",
        ));
    }

    if let Some(diagnostics) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    let descriptors = CanonicalSet::new(descriptors.into_values());
    let digest_input = (
        input.format_version,
        &input.target_digest,
        &native,
        &descriptors,
    );
    let matrix_digest = canonical_digest(DigestDomain::ConformancePlatformMatrix, &digest_input)
        .expect("validated platform matrix is canonically encodable");
    Ok(PortablePlatformMatrix {
        format_version: input.format_version,
        target_digest: input.target_digest,
        native_observations: native,
        descriptors,
        matrix_digest,
    })
}

/// Admits current Linux and macOS observations without claiming the Windows-complete matrix.
pub fn assemble_development_native_platform_set(
    target_digest: TargetDigest,
    observations: Vec<NativePlatformObservation>,
) -> Result<DevelopmentNativePlatformSet, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    let mut native = BTreeMap::new();
    let expected_domains = required_native_platform_domains();
    let expected_version = SemverVersion::new("1.97.1").expect("checked Rust version is valid");
    let mut shared_identities: Option<(Digest, Digest, Digest)> = None;
    for observation in observations {
        let identities = (
            observation.source_digest.clone(),
            observation.lockfiles_digest.clone(),
            observation.test_digest.clone(),
        );
        let identity_matches = shared_identities
            .as_ref()
            .is_none_or(|expected| expected == &identities);
        shared_identities.get_or_insert(identities);
        if !matches!(
            observation.platform.operating_system,
            OperatingSystem::Linux | OperatingSystem::Macos
        ) || observation.rust_version != expected_version
            || observation.domains.iter().copied().collect::<BTreeSet<_>>() != expected_domains
            || observation.outcome != NativeJobOutcome::Passed
            || !observation.native_execution
            || observation.link_mechanism != NativeLinkMechanism::PosixSymlink
            || observation.dagger_invocations != 0
            || observation.engine_starts != 0
            || observation.docker_invocations != 0
            || observation.other_sdk_invocations != 0
            || !identity_matches
        {
            diagnostics.push(platform_diagnostic(
                ConformanceDiagnosticCode::PlatformMatrixIncomplete,
                "development native observation is stale failed mismatched or non-native",
            ));
        }
        let os = observation.platform.operating_system.clone();
        if native.insert(os, observation).is_some() {
            diagnostics.push(platform_diagnostic(
                ConformanceDiagnosticCode::PlatformMatrixIncomplete,
                "development native operating-system observation is duplicated",
            ));
        }
    }
    let expected = [OperatingSystem::Linux, OperatingSystem::Macos]
        .into_iter()
        .collect::<BTreeSet<_>>();
    if native.keys().cloned().collect::<BTreeSet<_>>() != expected {
        diagnostics.push(platform_diagnostic(
            ConformanceDiagnosticCode::PlatformMatrixIncomplete,
            "development native observation set requires exact Linux and macOS evidence",
        ));
    }
    if let Some(diagnostics) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    let observation_set_digest = canonical_digest(
        DigestDomain::ConformancePlatformMatrix,
        &("development-native-set", &target_digest, &native),
    )
    .expect("validated development observation set is canonically encodable");
    Ok(DevelopmentNativePlatformSet {
        format_version: ConformanceFormatVersion::V1,
        target_digest,
        native_observations: native,
        observation_set_digest,
    })
}

/// Requires the initial artifact and verdict to bind the same Linux/amd64 platform.
pub fn admit_exact_engine_platform_claim(
    claim: &ExactEnginePlatformClaim,
) -> Result<(), ConformanceDiagnosticSet> {
    let initial = PlatformDescriptor::linux_amd64();
    if claim.artifact_platform != claim.verdict_platform
        || (claim.initial_signoff && claim.artifact_platform != initial)
    {
        return Err(ConformanceDiagnosticSet::new([platform_diagnostic(
            ConformanceDiagnosticCode::PlatformClaimInvalid,
            "artifact and verdict platform claim is widened or incompatible",
        )])
        .expect("platform claim diagnostic is non-empty"));
    }
    Ok(())
}

fn required_operating_systems() -> BTreeSet<OperatingSystem> {
    [
        OperatingSystem::Linux,
        OperatingSystem::Macos,
        OperatingSystem::Windows,
    ]
    .into_iter()
    .collect()
}

fn required_platforms() -> BTreeSet<PlatformDescriptor> {
    required_operating_systems()
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

fn platform_diagnostic(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Platform),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn descriptors_match_the_production_release_contract() {
        let version = SemverVersion::new("1.0.0-beta.10").unwrap();
        let descriptors = release_descriptor_matrix(&version);
        assert_eq!(descriptors.len(), 6);
        assert!(descriptors.iter().any(|descriptor| {
            descriptor.archive_name == "dagger_v1.0.0-beta.10_darwin_arm64.tar.gz"
                && descriptor.executable_member == "dagger"
        }));
        assert!(descriptors.iter().any(|descriptor| {
            descriptor.archive_name == "dagger_v1.0.0-beta.10_windows_amd64.zip"
                && descriptor.executable_member == "dagger.exe"
        }));
    }

    #[test]
    fn unknown_descriptor_aliases_fail_during_decode() {
        let value = serde_json::json!({
            "platform": {"operating_system": "darwin", "architecture": "x86_64"},
            "archive_name": "dagger.zip",
            "executable_member": "dagger",
            "archive_format": "zip"
        });
        assert!(serde_json::from_value::<ReleaseArchiveDescriptor>(value).is_err());
    }
}
