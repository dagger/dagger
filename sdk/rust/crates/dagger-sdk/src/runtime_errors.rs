//! Public runtime failures whose payloads remain safe under ordinary formatting.
//!
//! Internal adapters retain third-party causes, raw responses, diagnostics, and
//! command output at typed inspection boundaries. `Display` and `Debug` expose only
//! stable semantic coordinates, except for the engine-authored execution message which
//! is the public domain error itself.

use std::error::Error;
use std::fmt;
use std::sync::Arc;

use semver::Version;
use serde_json::{Map, Value};

use crate::graphql::RawResponse;

type SharedError = Arc<dyn Error + Send + Sync + 'static>;

/// Stable stage for a CLI acquisition failure.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ProvisioningErrorKind {
    /// Fixed release URL or target metadata was invalid.
    Target,
    /// Checksum-manifest acquisition or validation failed.
    Manifest,
    /// Release archive acquisition failed.
    Archive,
    /// Archive integrity or member validation failed.
    Integrity,
    /// Native cache validation, locking, or publication failed.
    Cache,
    /// The only permitted compatibility PATH transition also failed.
    CompatibilityFallback,
    /// Acquisition was cancelled.
    Cancelled,
}

impl ProvisioningErrorKind {
    const fn description(self) -> &'static str {
        match self {
            Self::Target => "the compiled Dagger CLI target is invalid",
            Self::Manifest => "the Dagger CLI checksum manifest could not be accepted",
            Self::Archive => "the Dagger CLI archive could not be acquired",
            Self::Integrity => "the Dagger CLI archive could not be verified",
            Self::Cache => "the Dagger CLI cache transaction failed",
            Self::CompatibilityFallback => {
                "the compiled Dagger CLI and compatibility lookup both failed"
            }
            Self::Cancelled => "Dagger CLI provisioning was cancelled",
        }
    }
}

/// Cloneable CLI acquisition failure with bounded safe coordinates.
#[derive(Clone)]
pub struct ProvisioningError {
    kind: ProvisioningErrorKind,
    status: Option<u16>,
    source: Option<SharedError>,
}

impl ProvisioningError {
    pub(crate) fn new(
        kind: ProvisioningErrorKind,
        status: Option<u16>,
        source: Option<SharedError>,
    ) -> Self {
        Self {
            kind,
            status,
            source,
        }
    }

    /// Returns the stable acquisition stage.
    pub const fn kind(&self) -> ProvisioningErrorKind {
        self.kind
    }

    /// Returns a release-server status when that is safe and relevant.
    pub const fn http_status(&self) -> Option<u16> {
        self.status
    }
}

impl fmt::Display for ProvisioningError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.kind.description())
    }
}

impl fmt::Debug for ProvisioningError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProvisioningError")
            .field("kind", &self.kind)
            .field("http_status", &self.status)
            .finish_non_exhaustive()
    }
}

impl Error for ProvisioningError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.source
            .as_deref()
            .map(|source| source as &(dyn Error + 'static))
    }
}

impl From<crate::provisioning_error::ProvisionError> for ProvisioningError {
    fn from(error: crate::provisioning_error::ProvisionError) -> Self {
        use crate::provisioning_error::ProvisionErrorKind as Internal;

        let kind = match error.kind() {
            Internal::InvalidReleaseUrl => ProvisioningErrorKind::Target,
            Internal::ManifestTransport
            | Internal::ReleaseUnavailable
            | Internal::ManifestStatus
            | Internal::ManifestRead
            | Internal::ManifestTooLarge
            | Internal::ManifestEncoding
            | Internal::ManifestSyntax
            | Internal::MissingChecksum
            | Internal::InvalidChecksum
            | Internal::AmbiguousChecksum => ProvisioningErrorKind::Manifest,
            Internal::ArchiveTransport
            | Internal::ArchiveStatus
            | Internal::ArchiveRead
            | Internal::ArchiveTooLarge => ProvisioningErrorKind::Archive,
            Internal::ChecksumMismatch
            | Internal::ArchiveFormat
            | Internal::MissingMember
            | Internal::AmbiguousMember
            | Internal::UnsafeMember
            | Internal::ExecutableTooLarge => ProvisioningErrorKind::Integrity,
            Internal::CacheDirectory
            | Internal::CacheEntrySymlink
            | Internal::CacheEntryNotRegular
            | Internal::CacheEntryPermissions
            | Internal::CacheLock
            | Internal::CachePublication => ProvisioningErrorKind::Cache,
            Internal::Cancelled => ProvisioningErrorKind::Cancelled,
        };
        let status = error.status();
        Self::new(kind, status, Some(Arc::new(error)))
    }
}

/// Stable stage for a native CLI start failure.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SessionStartupErrorKind {
    /// Native process creation failed after bounded retry policy.
    Spawn,
    /// The first stdout control record was invalid or unavailable.
    ControlProtocol,
}

/// Cloneable process-start failure which never renders captured process output.
#[derive(Clone)]
pub struct SessionStartupError {
    kind: SessionStartupErrorKind,
    source: SharedError,
}

impl SessionStartupError {
    pub(crate) fn with_source<E>(kind: SessionStartupErrorKind, source: E) -> Self
    where
        E: Error + Send + Sync + 'static,
    {
        Self {
            kind,
            source: Arc::new(source),
        }
    }

    /// Returns the stable process-start stage.
    pub const fn kind(&self) -> SessionStartupErrorKind {
        self.kind
    }
}

impl fmt::Display for SessionStartupError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self.kind {
            SessionStartupErrorKind::Spawn => "the Dagger CLI process could not be started",
            SessionStartupErrorKind::ControlProtocol => {
                "the Dagger CLI session control record is invalid"
            }
        })
    }
}

impl fmt::Debug for SessionStartupError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("SessionStartupError")
            .field("kind", &self.kind)
            .finish_non_exhaustive()
    }
}

impl Error for SessionStartupError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(self.source.as_ref())
    }
}

/// Why an engine identity could not prove the exact compiled target.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CompatibilityEvidenceGap {
    /// The compatibility request did not complete.
    Transport,
    /// The engine returned GraphQL errors.
    GraphQl,
    /// The response data had an unexpected shape.
    ResponseShape,
    /// The version value was absent.
    MissingVersion,
    /// The version was not valid Dagger semantic-version syntax.
    MalformedVersion,
    /// The version did not contain source-revision provenance.
    MissingRevision,
    /// The source identity explicitly described a dirty build.
    DirtyRevision,
    /// Build metadata used an unknown provenance format.
    UnknownRevisionFormat,
}

/// Stable compatibility result category.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CompatibilityErrorKind {
    /// The engine semantic version differs from the compiled target.
    VersionMismatch,
    /// A well-formed clean revision prefix differs from the compiled target.
    RevisionMismatch,
    /// Available evidence cannot prove or disprove the exact target.
    Unverified,
}

/// Safe exact-target compatibility failure for an implicit session.
#[derive(Clone)]
pub struct CompatibilityError {
    inner: Arc<CompatibilityErrorState>,
}

struct CompatibilityErrorState {
    kind: CompatibilityErrorKind,
    gap: Option<CompatibilityEvidenceGap>,
    expected_version: Version,
    observed_version: Option<Version>,
    expected_revision_prefix: String,
    observed_revision_prefix: Option<String>,
    source: Option<SharedError>,
}

impl CompatibilityError {
    pub(crate) fn mismatch(
        kind: CompatibilityErrorKind,
        expected_version: Version,
        observed_version: Option<Version>,
        expected_revision_prefix: String,
        observed_revision_prefix: Option<String>,
    ) -> Self {
        Self {
            inner: Arc::new(CompatibilityErrorState {
                kind,
                gap: None,
                expected_version,
                observed_version,
                expected_revision_prefix,
                observed_revision_prefix,
                source: None,
            }),
        }
    }

    pub(crate) fn unverified(
        gap: CompatibilityEvidenceGap,
        expected_version: Version,
        expected_revision_prefix: String,
        source: Option<SharedError>,
    ) -> Self {
        Self {
            inner: Arc::new(CompatibilityErrorState {
                kind: CompatibilityErrorKind::Unverified,
                gap: Some(gap),
                expected_version,
                observed_version: None,
                expected_revision_prefix,
                observed_revision_prefix: None,
                source,
            }),
        }
    }

    pub(crate) fn unverified_observed(
        gap: CompatibilityEvidenceGap,
        expected_version: Version,
        observed_version: Version,
        expected_revision_prefix: String,
    ) -> Self {
        Self {
            inner: Arc::new(CompatibilityErrorState {
                kind: CompatibilityErrorKind::Unverified,
                gap: Some(gap),
                expected_version,
                observed_version: Some(observed_version),
                expected_revision_prefix,
                observed_revision_prefix: None,
                source: None,
            }),
        }
    }

    /// Returns whether evidence proved a version mismatch, revision mismatch, or gap.
    pub fn kind(&self) -> CompatibilityErrorKind {
        self.inner.kind
    }

    /// Returns the evidence gap for an unverified identity.
    pub fn evidence_gap(&self) -> Option<CompatibilityEvidenceGap> {
        self.inner.gap
    }

    /// Returns the generated semantic target expected by this SDK build.
    pub fn expected_version(&self) -> &Version {
        &self.inner.expected_version
    }

    /// Returns a safely parsed observed semantic version when available.
    pub fn observed_version(&self) -> Option<&Version> {
        self.inner.observed_version.as_ref()
    }

    /// Returns the generated clean source-revision prefix.
    pub fn expected_revision_prefix(&self) -> &str {
        &self.inner.expected_revision_prefix
    }

    /// Returns a well-formed observed clean revision prefix when available.
    pub fn observed_revision_prefix(&self) -> Option<&str> {
        self.inner.observed_revision_prefix.as_deref()
    }
}

impl fmt::Display for CompatibilityError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self.inner.kind {
            CompatibilityErrorKind::VersionMismatch => {
                "the engine version does not match the Rust SDK target"
            }
            CompatibilityErrorKind::RevisionMismatch => {
                "the engine revision does not match the Rust SDK target"
            }
            CompatibilityErrorKind::Unverified => {
                "the engine identity cannot prove the Rust SDK target"
            }
        })
    }
}

impl fmt::Debug for CompatibilityError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("CompatibilityError")
            .field("kind", &self.inner.kind)
            .field("evidence_gap", &self.inner.gap)
            .field("expected_version", &self.inner.expected_version)
            .field("observed_version", &self.inner.observed_version)
            .field(
                "expected_revision_prefix",
                &self.inner.expected_revision_prefix,
            )
            .field(
                "observed_revision_prefix",
                &self.inner.observed_revision_prefix,
            )
            .finish_non_exhaustive()
    }
}

impl Error for CompatibilityError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.inner
            .source
            .as_deref()
            .map(|source| source as &(dyn Error + 'static))
    }
}

/// One deterministic failure category observed while releasing a CLI session.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum ShutdownFailureKind {
    /// The child did not exit within the graceful shutdown bound.
    Timeout,
    /// Forced termination could not be started.
    Kill,
    /// The child could not be waited for or reaped.
    Reap,
    /// The child exited unsuccessfully before the graceful bound.
    UnexpectedExit,
    /// The stdout worker failed or could not be joined.
    Stdout,
    /// The stderr worker failed or could not be joined.
    Stderr,
}

/// Aggregate shutdown failure preserving every safe component category once.
#[derive(Clone, Eq, PartialEq)]
pub struct ShutdownError {
    failures: Arc<[ShutdownFailureKind]>,
    stdout_tail: Arc<[u8]>,
    stderr_tail: Arc<[u8]>,
}

impl ShutdownError {
    #[cfg(test)]
    pub(crate) fn new(mut failures: Vec<ShutdownFailureKind>) -> Self {
        failures.sort_unstable();
        failures.dedup();
        Self {
            failures: Arc::from(failures),
            stdout_tail: Arc::from([]),
            stderr_tail: Arc::from([]),
        }
    }

    pub(crate) fn with_diagnostics(
        mut failures: Vec<ShutdownFailureKind>,
        stdout_tail: Vec<u8>,
        stderr_tail: Vec<u8>,
    ) -> Self {
        failures.sort_unstable();
        failures.dedup();
        Self {
            failures: Arc::from(failures),
            stdout_tail: Arc::from(stdout_tail),
            stderr_tail: Arc::from(stderr_tail),
        }
    }

    /// Returns failures in deterministic category order.
    pub fn failures(&self) -> &[ShutdownFailureKind] {
        &self.failures
    }

    /// Returns the already-redacted bounded stdout diagnostic tail.
    pub fn stdout_tail(&self) -> &[u8] {
        &self.stdout_tail
    }

    /// Returns the already-redacted bounded stderr diagnostic tail.
    pub fn stderr_tail(&self) -> &[u8] {
        &self.stderr_tail
    }
}

impl fmt::Display for ShutdownError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("the owned Dagger session did not shut down cleanly")
    }
}

impl fmt::Debug for ShutdownError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ShutdownError")
            .field("failures", &self.failures)
            .finish()
    }
}

impl Error for ShutdownError {}

/// Typed engine-authored failure from an exec operation.
#[derive(Clone)]
pub struct ExecError {
    inner: Arc<ExecErrorState>,
}

struct ExecErrorState {
    message: String,
    exit_code: Option<i32>,
    command: Option<Vec<String>>,
    stdout: Option<String>,
    stderr: Option<String>,
    extensions: Map<String, Value>,
}

impl ExecError {
    pub(crate) fn from_response(response: &RawResponse) -> Option<Self> {
        let error = response.errors().iter().find(|error| {
            error
                .extensions()
                .and_then(|extensions| extensions.get("_type"))
                .and_then(Value::as_str)
                == Some("EXEC_ERROR")
        })?;
        let extensions = error.extensions()?.clone();
        let exit_code = optional_i32(&extensions, "exitCode")?;
        let command = optional_string_array(&extensions, "cmd")?;
        let stdout = optional_string(&extensions, "stdout")?;
        let stderr = optional_string(&extensions, "stderr")?;
        Some(Self {
            inner: Arc::new(ExecErrorState {
                message: error.message().to_owned(),
                exit_code,
                command,
                stdout,
                stderr,
                extensions,
            }),
        })
    }

    /// Returns the engine-authored execution failure message.
    pub fn message(&self) -> &str {
        &self.inner.message
    }

    /// Returns the process exit code when supplied by the engine.
    pub fn exit_code(&self) -> Option<i32> {
        self.inner.exit_code
    }

    /// Returns the executed argument vector when supplied by the engine.
    pub fn command(&self) -> Option<&[String]> {
        self.inner.command.as_deref()
    }

    /// Returns captured standard output without appending it to error formatting.
    pub fn stdout(&self) -> Option<&str> {
        self.inner.stdout.as_deref()
    }

    /// Returns captured standard error without appending it to error formatting.
    pub fn stderr(&self) -> Option<&str> {
        self.inner.stderr.as_deref()
    }

    /// Returns the complete extension object, including unknown future members.
    pub fn extensions(&self) -> &Map<String, Value> {
        &self.inner.extensions
    }
}

impl fmt::Display for ExecError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.inner.message)
    }
}

impl fmt::Debug for ExecError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ExecError")
            .field("exit_code", &self.inner.exit_code)
            .field("command_present", &self.inner.command.is_some())
            .field("stdout_present", &self.inner.stdout.is_some())
            .field("stderr_present", &self.inner.stderr.is_some())
            .field("extension_count", &self.inner.extensions.len())
            .finish_non_exhaustive()
    }
}

impl Error for ExecError {}

fn optional_i32(extensions: &Map<String, Value>, key: &str) -> Option<Option<i32>> {
    match extensions.get(key) {
        None => Some(None),
        Some(Value::Number(number)) => number
            .as_i64()
            .and_then(|value| i32::try_from(value).ok())
            .map(Some),
        Some(_) => None,
    }
}

fn optional_string_array(
    extensions: &Map<String, Value>,
    key: &str,
) -> Option<Option<Vec<String>>> {
    match extensions.get(key) {
        None => Some(None),
        Some(Value::Array(values)) => values
            .iter()
            .map(|value| value.as_str().map(str::to_owned))
            .collect::<Option<Vec<_>>>()
            .map(Some),
        Some(_) => None,
    }
}

fn optional_string(extensions: &Map<String, Value>, key: &str) -> Option<Option<String>> {
    match extensions.get(key) {
        None => Some(None),
        Some(Value::String(value)) => Some(Some(value.clone())),
        Some(_) => None,
    }
}
