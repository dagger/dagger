//! Exact Dagger target identity and platform-specific release descriptors.
//!
//! The checked constants are generated from the completeness target. Runtime parsing
//! happens once, giving later provisioning stages validated semantic versions and a
//! fixed revision rather than repeatedly interpreting repository metadata.

use std::sync::OnceLock;

use semver::Version;
use url::Url;

use crate::errors::{PlatformError, PlatformErrorKind, TargetError, TargetErrorKind};
use crate::target_generated::{TARGET_CLI_VERSION, TARGET_ENGINE_VERSION, TARGET_REVISION};

const RELEASE_ORIGIN: &str = "https://dl.dagger.io/dagger/releases/";

/// One validated, immutable repository target.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct ExactTarget {
    engine_version: Version,
    cli_version: Version,
    revision: DaggerRevision,
}

impl ExactTarget {
    fn parse(engine_version: &str, cli_version: &str, revision: &str) -> Result<Self, TargetError> {
        let engine_version = engine_version
            .strip_prefix('v')
            .ok_or_else(|| TargetError::new(TargetErrorKind::InvalidEngineVersion))
            .and_then(|version| {
                Version::parse(version)
                    .map_err(|_| TargetError::new(TargetErrorKind::InvalidEngineVersion))
            })?;
        let cli_version = Version::parse(cli_version)
            .map_err(|_| TargetError::new(TargetErrorKind::InvalidCliVersion))?;
        let revision = DaggerRevision::parse(revision)?;

        if engine_version != cli_version {
            return Err(TargetError::new(TargetErrorKind::VersionMismatch));
        }

        Ok(Self {
            engine_version,
            cli_version,
            revision,
        })
    }

    pub(crate) fn engine_version(&self) -> &Version {
        &self.engine_version
    }

    pub(crate) fn cli_version(&self) -> &Version {
        &self.cli_version
    }

    pub(crate) fn revision(&self) -> &DaggerRevision {
        &self.revision
    }
}

/// The exact Dagger source revision represented as decoded bytes.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct DaggerRevision([u8; 20]);

impl DaggerRevision {
    fn parse(value: &str) -> Result<Self, TargetError> {
        if value.len() != 40 {
            return Err(TargetError::new(TargetErrorKind::InvalidRevision));
        }

        let mut bytes = [0_u8; 20];
        for (index, pair) in value.as_bytes().chunks_exact(2).enumerate() {
            let high = decode_hex(pair[0])
                .ok_or_else(|| TargetError::new(TargetErrorKind::InvalidRevision))?;
            let low = decode_hex(pair[1])
                .ok_or_else(|| TargetError::new(TargetErrorKind::InvalidRevision))?;
            bytes[index] = (high << 4) | low;
        }
        Ok(Self(bytes))
    }

    pub(crate) fn bytes(&self) -> &[u8; 20] {
        &self.0
    }
}

fn decode_hex(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        _ => None,
    }
}

/// Returns the process-wide parsed exact target.
pub(crate) fn exact_target() -> Result<&'static ExactTarget, TargetError> {
    static TARGET: OnceLock<Result<ExactTarget, TargetError>> = OnceLock::new();
    TARGET
        .get_or_init(|| {
            ExactTarget::parse(TARGET_ENGINE_VERSION, TARGET_CLI_VERSION, TARGET_REVISION)
        })
        .as_ref()
        .map_err(Clone::clone)
}

/// Operating-system coordinate used by Dagger release artifacts.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum OperatingSystem {
    Linux,
    Darwin,
    Windows,
}

impl OperatingSystem {
    pub(crate) fn parse(value: &str) -> Result<Self, PlatformError> {
        match value {
            "linux" => Ok(Self::Linux),
            "macos" | "darwin" => Ok(Self::Darwin),
            "windows" => Ok(Self::Windows),
            _ => Err(PlatformError::new(
                PlatformErrorKind::UnsupportedOperatingSystem,
            )),
        }
    }

    const fn release_name(self) -> &'static str {
        match self {
            Self::Linux => "linux",
            Self::Darwin => "darwin",
            Self::Windows => "windows",
        }
    }
}

/// CPU coordinate used by Dagger release artifacts.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum Architecture {
    Amd64,
    Arm64,
}

impl Architecture {
    pub(crate) fn parse(value: &str) -> Result<Self, PlatformError> {
        match value {
            "x86_64" | "amd64" => Ok(Self::Amd64),
            "aarch64" | "arm64" => Ok(Self::Arm64),
            _ => Err(PlatformError::new(
                PlatformErrorKind::UnsupportedArchitecture,
            )),
        }
    }

    const fn release_name(self) -> &'static str {
        match self {
            Self::Amd64 => "amd64",
            Self::Arm64 => "arm64",
        }
    }
}

/// Container format selected by the release platform.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ArchiveFormat {
    TarGz,
    Zip,
}

/// Complete fixed-origin release description used by provisioning.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct ArchiveDescriptor {
    archive_name: String,
    member_name: &'static str,
    format: ArchiveFormat,
    manifest_url: Url,
    archive_url: Url,
}

impl ArchiveDescriptor {
    pub(crate) fn for_native_target(target: &ExactTarget) -> Result<Self, PlatformError> {
        let operating_system = OperatingSystem::parse(std::env::consts::OS)?;
        let architecture = Architecture::parse(std::env::consts::ARCH)?;
        Self::for_target(target, operating_system, architecture)
    }

    pub(crate) fn for_target(
        target: &ExactTarget,
        operating_system: OperatingSystem,
        architecture: Architecture,
    ) -> Result<Self, PlatformError> {
        let (format, extension, member_name) = match operating_system {
            OperatingSystem::Linux | OperatingSystem::Darwin => {
                (ArchiveFormat::TarGz, "tar.gz", "dagger")
            }
            OperatingSystem::Windows => (ArchiveFormat::Zip, "zip", "dagger.exe"),
        };
        let archive_name = format!(
            "dagger_v{}_{}_{}.{}",
            target.cli_version(),
            operating_system.release_name(),
            architecture.release_name(),
            extension
        );
        let base = format!("{RELEASE_ORIGIN}{}/", target.cli_version());
        let manifest_url = Url::parse(&format!("{base}checksums.txt"))
            .map_err(|_| PlatformError::new(PlatformErrorKind::InvalidDescriptor))?;
        let archive_url = Url::parse(&format!("{base}{archive_name}"))
            .map_err(|_| PlatformError::new(PlatformErrorKind::InvalidDescriptor))?;

        Ok(Self {
            archive_name,
            member_name,
            format,
            manifest_url,
            archive_url,
        })
    }

    pub(crate) fn archive_name(&self) -> &str {
        &self.archive_name
    }

    pub(crate) fn cli_version(&self) -> Result<Version, PlatformError> {
        let version = self
            .archive_name
            .strip_prefix("dagger_v")
            .and_then(|value| value.split_once('_').map(|(version, _)| version))
            .ok_or_else(|| PlatformError::new(PlatformErrorKind::InvalidDescriptor))?;
        Version::parse(version)
            .map_err(|_| PlatformError::new(PlatformErrorKind::InvalidDescriptor))
    }

    pub(crate) const fn member_name(&self) -> &'static str {
        self.member_name
    }

    pub(crate) const fn format(&self) -> ArchiveFormat {
        self.format
    }

    pub(crate) fn manifest_url(&self) -> &Url {
        &self.manifest_url
    }

    pub(crate) fn archive_url(&self) -> &Url {
        &self.archive_url
    }
}

#[cfg(test)]
pub(crate) fn exact_target_from_parts(
    engine_version: &str,
    cli_version: &str,
    revision: &str,
) -> Result<ExactTarget, TargetError> {
    ExactTarget::parse(engine_version, cli_version, revision)
}
