//! Credential-safe failures for private CLI acquisition and cache publication.
//!
//! The provisioning implementation reduces third-party errors to stable phase kinds at
//! their boundary. Ordinary formatting never includes URLs, response bodies, cache
//! paths, or opaque source text; callers inside the SDK can still inspect safe status
//! and checksum coordinates without weakening that disclosure contract.

use std::error::Error;
use std::fmt;
use std::sync::Arc;

type SharedError = Arc<dyn Error + Send + Sync + 'static>;

/// A stable internal category for CLI provisioning failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ProvisionErrorKind {
    InvalidReleaseUrl,
    ManifestTransport,
    ReleaseUnavailable,
    ManifestStatus,
    ManifestRead,
    ManifestTooLarge,
    ManifestEncoding,
    ManifestSyntax,
    MissingChecksum,
    InvalidChecksum,
    AmbiguousChecksum,
    ArchiveTransport,
    ArchiveStatus,
    ArchiveRead,
    ArchiveTooLarge,
    ChecksumMismatch,
    ArchiveFormat,
    MissingMember,
    AmbiguousMember,
    UnsafeMember,
    ExecutableTooLarge,
    CacheDirectory,
    CacheEntrySymlink,
    CacheEntryNotRegular,
    CacheEntryPermissions,
    CacheLock,
    CachePublication,
    Cancelled,
}

impl ProvisionErrorKind {
    const fn description(self) -> &'static str {
        match self {
            Self::InvalidReleaseUrl => "the fixed Dagger release URL is invalid",
            Self::ManifestTransport => "the Dagger checksum manifest request failed",
            Self::ReleaseUnavailable => "the compiled Dagger CLI release is unavailable",
            Self::ManifestStatus => "the Dagger checksum manifest returned an unexpected status",
            Self::ManifestRead => "the Dagger checksum manifest could not be read",
            Self::ManifestTooLarge => "the Dagger checksum manifest exceeds its size limit",
            Self::ManifestEncoding => "the Dagger checksum manifest is not UTF-8",
            Self::ManifestSyntax => "the Dagger checksum manifest has invalid syntax",
            Self::MissingChecksum => "the Dagger release checksum is missing",
            Self::InvalidChecksum => "the Dagger release checksum is invalid",
            Self::AmbiguousChecksum => "the Dagger release checksum is ambiguous",
            Self::ArchiveTransport => "the Dagger CLI archive request failed",
            Self::ArchiveStatus => "the Dagger CLI archive returned an unexpected status",
            Self::ArchiveRead => "the Dagger CLI archive could not be read",
            Self::ArchiveTooLarge => "the Dagger CLI archive exceeds its size limit",
            Self::ChecksumMismatch => "the Dagger CLI archive checksum does not match",
            Self::ArchiveFormat => "the Dagger CLI archive format is invalid",
            Self::MissingMember => "the Dagger CLI executable is missing from the archive",
            Self::AmbiguousMember => "the Dagger CLI executable is duplicated in the archive",
            Self::UnsafeMember => "the Dagger CLI archive contains an unsafe member",
            Self::ExecutableTooLarge => "the Dagger CLI executable exceeds its size limit",
            Self::CacheDirectory => "the Dagger CLI cache directory is unavailable",
            Self::CacheEntrySymlink => "the Dagger CLI cache entry is a symbolic link",
            Self::CacheEntryNotRegular => "the Dagger CLI cache entry is not a regular file",
            Self::CacheEntryPermissions => "the Dagger CLI cache entry permissions are unsafe",
            Self::CacheLock => "the Dagger CLI cache lock failed",
            Self::CachePublication => "the Dagger CLI cache publication failed",
            Self::Cancelled => "Dagger CLI provisioning was cancelled",
        }
    }
}

/// A bounded internal provisioning error with optional safe coordinates.
#[derive(Clone)]
pub(crate) struct ProvisionError {
    kind: ProvisionErrorKind,
    status: Option<u16>,
    expected_digest: Option<[u8; 32]>,
    actual_digest: Option<[u8; 32]>,
    source: Option<SharedError>,
}

impl ProvisionError {
    pub(crate) const fn new(kind: ProvisionErrorKind) -> Self {
        Self {
            kind,
            status: None,
            expected_digest: None,
            actual_digest: None,
            source: None,
        }
    }

    pub(crate) const fn with_status(kind: ProvisionErrorKind, status: u16) -> Self {
        Self {
            kind,
            status: Some(status),
            expected_digest: None,
            actual_digest: None,
            source: None,
        }
    }

    pub(crate) fn with_source<E>(kind: ProvisionErrorKind, source: E) -> Self
    where
        E: Error + Send + Sync + 'static,
    {
        Self {
            kind,
            status: None,
            expected_digest: None,
            actual_digest: None,
            source: Some(Arc::new(source)),
        }
    }

    pub(crate) const fn checksum_mismatch(expected: [u8; 32], actual: [u8; 32]) -> Self {
        Self {
            kind: ProvisionErrorKind::ChecksumMismatch,
            status: None,
            expected_digest: Some(expected),
            actual_digest: Some(actual),
            source: None,
        }
    }

    pub(crate) const fn kind(&self) -> ProvisionErrorKind {
        self.kind
    }

    pub(crate) const fn status(&self) -> Option<u16> {
        self.status
    }

    pub(crate) const fn expected_digest(&self) -> Option<&[u8; 32]> {
        self.expected_digest.as_ref()
    }

    pub(crate) const fn actual_digest(&self) -> Option<&[u8; 32]> {
        self.actual_digest.as_ref()
    }
}

impl fmt::Display for ProvisionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.kind.description())
    }
}

impl fmt::Debug for ProvisionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProvisionError")
            .field("kind", &self.kind)
            .field("status", &self.status)
            .field("expected_digest_present", &self.expected_digest.is_some())
            .field("actual_digest_present", &self.actual_digest.is_some())
            .finish_non_exhaustive()
    }
}

impl Error for ProvisionError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.source
            .as_deref()
            .map(|source| source as &(dyn Error + 'static))
    }
}
