//! Stable, phase-specific failures exposed by the Rust SDK client facade.
//!
//! Ordinary `Display` and `Debug` output is deliberately bounded to static messages
//! and safe enum coordinates. Opaque third-party causes remain available through
//! [`std::error::Error::source`] for callers who explicitly inspect them; they are
//! never interpolated into routine diagnostics where credentials or host data could
//! escape.

use std::error::Error;
use std::fmt;
use std::sync::Arc;
use std::time::Duration;

use crate::connection::{EngineConnectionError, EngineConnectionErrorKind};
use crate::graphql::RawResponse;

type SharedError = Arc<dyn Error + Send + Sync + 'static>;

/// The connection or request phase governed by a timeout value.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TimeoutPhase {
    /// Establishing a selected Dagger session.
    SessionStartup,
    /// Establishing the HTTP connection for a request.
    HttpConnect,
    /// Executing one complete GraphQL request.
    GraphQlExecution,
}

impl fmt::Display for TimeoutPhase {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::SessionStartup => "session startup",
            Self::HttpConnect => "HTTP connection",
            Self::GraphQlExecution => "GraphQL execution",
        })
    }
}

/// A configuration coordinate used by typed source-compatibility failures.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ConfigOption {
    /// Working-directory selection.
    Workdir,
    /// Workspace selection.
    Workspace,
    /// Progress and lifecycle diagnostic delivery.
    DiagnosticSink,
    /// Workspace-module loading.
    LoadWorkspaceModules,
    /// Engine schema version override.
    Version,
    /// CLI progress verbosity.
    Verbosity,
    /// Provisioned engine runner host.
    RunnerHost,
    /// Additional child-process environment.
    Environment,
    /// Session startup timeout.
    SessionStartupTimeout,
    /// SDK-owned HTTP connection timeout.
    HttpConnectTimeout,
}

impl fmt::Display for ConfigOption {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Workdir => "workdir",
            Self::Workspace => "workspace",
            Self::DiagnosticSink => "diagnostic sink",
            Self::LoadWorkspaceModules => "workspace-module loading",
            Self::Version => "version override",
            Self::Verbosity => "verbosity",
            Self::RunnerHost => "runner host",
            Self::Environment => "additional environment",
            Self::SessionStartupTimeout => "session startup timeout",
            Self::HttpConnectTimeout => "HTTP connect timeout",
        })
    }
}

/// A structurally invalid or source-incompatible client configuration.
#[non_exhaustive]
#[derive(Clone, Eq, PartialEq)]
pub enum ConfigError {
    /// The working-directory value is empty or is invalid at preflight.
    InvalidWorkdir,
    /// An explicitly configured workspace reference is empty.
    InvalidWorkspace,
    /// The engine schema version is not a `v`-prefixed or plain semantic version.
    InvalidVersion,
    /// The runner host is not an absolute URI.
    InvalidRunnerHost,
    /// A timeout for `phase` is zero.
    InvalidTimeout {
        /// The independently configured timeout phase.
        phase: TimeoutPhase,
    },
    /// The configured verbosity cannot be represented as a `u8`.
    VerbosityOutOfRange,
    /// An environment key at `index` is empty or contains `=` or NUL.
    InvalidEnvironmentKey {
        /// Position in the caller-authored environment list.
        index: usize,
    },
    /// Two environment keys compare equal ignoring ASCII case.
    DuplicateEnvironmentKey {
        /// Position of the first equivalent key.
        first: usize,
        /// Position of the later equivalent key.
        duplicate: usize,
    },
    /// An environment key is owned by Dagger session or trace configuration.
    ReservedEnvironmentKey {
        /// Position in the caller-authored environment list.
        index: usize,
    },
    /// An environment value contains NUL.
    InvalidEnvironmentValue {
        /// Position in the caller-authored environment list.
        index: usize,
    },
    /// A transferred connection was combined with a CLI-owned option.
    ExplicitConnectionConflict {
        /// The incompatible option.
        option: ConfigOption,
    },
    /// Existing-session selection cannot honor this option.
    ExistingSessionConflict {
        /// The incompatible option.
        option: ConfigOption,
    },
    /// The selected source cannot give this option any effect.
    OptionConflict {
        /// The ineffective option.
        option: ConfigOption,
    },
    /// A beta configuration field has no stable 1.0 representation.
    LegacyOptionRemoved,
}

impl fmt::Display for ConfigError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidWorkdir => formatter.write_str("the workdir is invalid"),
            Self::InvalidWorkspace => formatter.write_str("the workspace reference is invalid"),
            Self::InvalidVersion => formatter.write_str("the engine version is invalid"),
            Self::InvalidRunnerHost => formatter.write_str("the runner host is invalid"),
            Self::InvalidTimeout { phase } => write!(formatter, "the {phase} timeout is invalid"),
            Self::VerbosityOutOfRange => formatter.write_str("the verbosity is out of range"),
            Self::InvalidEnvironmentKey { .. } => {
                formatter.write_str("an additional environment key is invalid")
            }
            Self::DuplicateEnvironmentKey { .. } => {
                formatter.write_str("an additional environment key is duplicated")
            }
            Self::ReservedEnvironmentKey { .. } => {
                formatter.write_str("an additional environment key is reserved")
            }
            Self::InvalidEnvironmentValue { .. } => {
                formatter.write_str("an additional environment value is invalid")
            }
            Self::ExplicitConnectionConflict { option } => {
                write!(
                    formatter,
                    "an explicit connection is incompatible with {option}"
                )
            }
            Self::ExistingSessionConflict { option } => {
                write!(
                    formatter,
                    "an existing session is incompatible with {option}"
                )
            }
            Self::OptionConflict { option } => {
                write!(
                    formatter,
                    "the selected connection source cannot apply {option}"
                )
            }
            Self::LegacyOptionRemoved => {
                formatter.write_str("the beta configuration option was removed")
            }
        }
    }
}

impl fmt::Debug for ConfigError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidWorkdir => formatter.write_str("InvalidWorkdir"),
            Self::InvalidWorkspace => formatter.write_str("InvalidWorkspace"),
            Self::InvalidVersion => formatter.write_str("InvalidVersion"),
            Self::InvalidRunnerHost => formatter.write_str("InvalidRunnerHost"),
            Self::InvalidTimeout { phase } => formatter
                .debug_struct("InvalidTimeout")
                .field("phase", phase)
                .finish(),
            Self::VerbosityOutOfRange => formatter.write_str("VerbosityOutOfRange"),
            Self::InvalidEnvironmentKey { index } => formatter
                .debug_struct("InvalidEnvironmentKey")
                .field("index", index)
                .finish(),
            Self::DuplicateEnvironmentKey { first, duplicate } => formatter
                .debug_struct("DuplicateEnvironmentKey")
                .field("first", first)
                .field("duplicate", duplicate)
                .finish(),
            Self::ReservedEnvironmentKey { index } => formatter
                .debug_struct("ReservedEnvironmentKey")
                .field("index", index)
                .finish(),
            Self::InvalidEnvironmentValue { index } => formatter
                .debug_struct("InvalidEnvironmentValue")
                .field("index", index)
                .finish(),
            Self::ExplicitConnectionConflict { option } => formatter
                .debug_struct("ExplicitConnectionConflict")
                .field("option", option)
                .finish(),
            Self::ExistingSessionConflict { option } => formatter
                .debug_struct("ExistingSessionConflict")
                .field("option", option)
                .finish(),
            Self::OptionConflict { option } => formatter
                .debug_struct("OptionConflict")
                .field("option", option)
                .finish(),
            Self::LegacyOptionRemoved => formatter.write_str("LegacyOptionRemoved"),
        }
    }
}

impl Error for ConfigError {}

macro_rules! opaque_codec_error {
    ($name:ident, $kind:ident, $description:literal) => {
        #[doc = $description]
        #[derive(Clone)]
        pub struct $name {
            kind: $kind,
            source: Option<SharedError>,
        }

        impl $name {
            /// Creates an error without an underlying implementation-specific cause.
            pub fn new(kind: $kind) -> Self {
                Self { kind, source: None }
            }

            /// Creates an error which retains an opaque typed cause.
            pub fn with_source<E>(kind: $kind, source: E) -> Self
            where
                E: Error + Send + Sync + 'static,
            {
                Self {
                    kind,
                    source: Some(Arc::new(source)),
                }
            }

            /// Returns the stable failure category.
            pub const fn kind(&self) -> $kind {
                self.kind
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(self.kind.description())
            }
        }

        impl fmt::Debug for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter
                    .debug_struct(stringify!($name))
                    .field("kind", &self.kind)
                    .finish_non_exhaustive()
            }
        }

        impl Error for $name {
            fn source(&self) -> Option<&(dyn Error + 'static)> {
                self.source
                    .as_deref()
                    .map(|source| source as &(dyn Error + 'static))
            }
        }
    };
}

/// Stable categories for raw-request codec failures.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RequestEncodingErrorKind {
    /// JSON serialization or deserialization failed.
    Json,
    /// A decoded request did not have the required field shape.
    InvalidShape,
}

impl RequestEncodingErrorKind {
    const fn description(self) -> &'static str {
        match self {
            Self::Json => "the GraphQL request could not be encoded",
            Self::InvalidShape => "the GraphQL request has an invalid shape",
        }
    }
}

opaque_codec_error!(
    RequestEncodingError,
    RequestEncodingErrorKind,
    "A redacted raw-request encoding failure with an inspectable optional source."
);

/// Stable categories for raw-response codec failures.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ResponseDecodingErrorKind {
    /// JSON deserialization failed.
    Json,
    /// The response or a nested GraphQL error had an invalid shape.
    InvalidShape,
}

impl ResponseDecodingErrorKind {
    const fn description(self) -> &'static str {
        match self {
            Self::Json => "the GraphQL response could not be decoded",
            Self::InvalidShape => "the GraphQL response has an invalid shape",
        }
    }
}

opaque_codec_error!(
    ResponseDecodingError,
    ResponseDecodingErrorKind,
    "A redacted raw-response decoding failure with an inspectable optional source."
);

/// Stable categories for compositional-query construction failures.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum QueryBuildErrorKind {
    /// A GraphQL argument could not be serialized.
    ArgumentEncoding,
    /// A lazy identifier could not be resolved.
    LazyIdentifier,
    /// The selection cannot form a valid document.
    InvalidSelection,
}

impl QueryBuildErrorKind {
    const fn description(self) -> &'static str {
        match self {
            Self::ArgumentEncoding => "a query argument could not be encoded",
            Self::LazyIdentifier => "a lazy query identifier could not be resolved",
            Self::InvalidSelection => "the query selection is invalid",
        }
    }
}

opaque_codec_error!(
    QueryBuildError,
    QueryBuildErrorKind,
    "A redacted compositional-query construction failure."
);

/// Failure while establishing an owned client.
#[non_exhaustive]
#[derive(Clone)]
pub enum ConnectError {
    /// Pure configuration or preflight validation failed.
    Config(ConfigError),
    /// The selected connection did not become ready before the startup bound.
    StartupTimeout {
        /// The configured startup bound.
        duration: Duration,
    },
    /// Provisioning or connection establishment failed.
    Connection(EngineConnectionError),
    /// A transitional callback-scoped client callback failed.
    CallbackFailed(EngineConnectionError),
    /// Transitional callback cleanup failed.
    Close(CloseError),
}

impl fmt::Display for ConnectError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Config(_) => "the client configuration is invalid",
            Self::StartupTimeout { .. } => "the Dagger session did not start in time",
            Self::Connection(_) => "the Dagger connection could not be established",
            Self::CallbackFailed(_) => "the client callback failed",
            Self::Close(_) => "the client could not be closed",
        })
    }
}

impl fmt::Debug for ConnectError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Config(error) => formatter.debug_tuple("Config").field(error).finish(),
            Self::StartupTimeout { .. } => formatter.write_str("StartupTimeout"),
            Self::Connection(error) => formatter.debug_tuple("Connection").field(error).finish(),
            Self::CallbackFailed(error) => formatter
                .debug_tuple("CallbackFailed")
                .field(error)
                .finish(),
            Self::Close(error) => formatter.debug_tuple("Close").field(error).finish(),
        }
    }
}

impl Error for ConnectError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Config(error) => Some(error),
            Self::Connection(error) => Some(error),
            Self::CallbackFailed(error) => Some(error),
            Self::Close(error) => Some(error),
            Self::StartupTimeout { .. } => None,
        }
    }
}

impl From<ConfigError> for ConnectError {
    fn from(error: ConfigError) -> Self {
        Self::Config(error)
    }
}

/// Failure while executing one raw GraphQL request.
#[non_exhaustive]
#[derive(Clone)]
pub enum RequestError {
    /// The request could not be encoded before transport invocation.
    RequestEncoding(RequestEncodingError),
    /// The response could not be decoded without losing protocol information.
    ResponseDecoding(ResponseDecodingError),
    /// HTTP connection establishment exceeded its independent bound.
    TransportConnectTimeout {
        /// The configured HTTP-connect bound.
        duration: Duration,
    },
    /// Complete GraphQL execution exceeded its independent bound.
    ExecutionTimeout {
        /// The configured complete-request bound.
        duration: Duration,
    },
    /// The shared client lifecycle has started or completed shutdown.
    ClientClosed,
    /// An admitted request was interrupted by shared close.
    InterruptedByClose,
    /// The engine connection returned a typed failure.
    Connection(EngineConnectionError),
    /// An injected connection unwound while executing a request.
    ConnectionPanicked,
}

impl fmt::Display for RequestError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::RequestEncoding(_) => "the GraphQL request could not be encoded",
            Self::ResponseDecoding(_) => "the GraphQL response could not be decoded",
            Self::TransportConnectTimeout { .. } => "the HTTP connection timed out",
            Self::ExecutionTimeout { .. } => "the GraphQL request timed out",
            Self::ClientClosed => "the client is closed",
            Self::InterruptedByClose => "the request was interrupted by client close",
            Self::Connection(_) => "the engine connection failed",
            Self::ConnectionPanicked => "the engine connection panicked",
        })
    }
}

impl fmt::Debug for RequestError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::RequestEncoding(error) => formatter
                .debug_tuple("RequestEncoding")
                .field(error)
                .finish(),
            Self::ResponseDecoding(error) => formatter
                .debug_tuple("ResponseDecoding")
                .field(error)
                .finish(),
            Self::TransportConnectTimeout { .. } => formatter.write_str("TransportConnectTimeout"),
            Self::ExecutionTimeout { .. } => formatter.write_str("ExecutionTimeout"),
            Self::ClientClosed => formatter.write_str("ClientClosed"),
            Self::InterruptedByClose => formatter.write_str("InterruptedByClose"),
            Self::Connection(error) => formatter.debug_tuple("Connection").field(error).finish(),
            Self::ConnectionPanicked => formatter.write_str("ConnectionPanicked"),
        }
    }
}

impl Error for RequestError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::RequestEncoding(error) => Some(error),
            Self::ResponseDecoding(error) => Some(error),
            Self::Connection(error) => Some(error),
            Self::TransportConnectTimeout { .. }
            | Self::ExecutionTimeout { .. }
            | Self::ClientClosed
            | Self::InterruptedByClose
            | Self::ConnectionPanicked => None,
        }
    }
}

/// Failure while building or decoding a generated/compositional query.
#[non_exhaustive]
#[derive(Clone)]
pub enum QueryError {
    /// Query construction failed before request execution.
    Build(QueryBuildError),
    /// Raw request execution failed.
    Request(RequestError),
    /// The engine returned GraphQL errors; partial data remains in the response.
    GraphQl {
        /// The complete raw response, including partial data and extensions.
        response: RawResponse,
    },
    /// Selected response data did not match the requested Rust type.
    Decode(ResponseDecodingError),
}

impl fmt::Display for QueryError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Build(_) => "the GraphQL query could not be built",
            Self::Request(_) => "the GraphQL query request failed",
            Self::GraphQl { .. } => "the engine returned GraphQL errors",
            Self::Decode(_) => "the selected GraphQL data could not be decoded",
        })
    }
}

impl fmt::Debug for QueryError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Build(error) => formatter.debug_tuple("Build").field(error).finish(),
            Self::Request(error) => formatter.debug_tuple("Request").field(error).finish(),
            Self::GraphQl { response } => formatter
                .debug_struct("GraphQl")
                .field("error_count", &response.errors().len())
                .field("data_kind", &response.data().kind_name())
                .finish(),
            Self::Decode(error) => formatter.debug_tuple("Decode").field(error).finish(),
        }
    }
}

impl Error for QueryError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Build(error) => Some(error),
            Self::Request(error) => Some(error),
            Self::Decode(error) => Some(error),
            Self::GraphQl { .. } => None,
        }
    }
}

/// The cloneable terminal result of the shared close attempt.
#[non_exhaustive]
#[derive(Clone)]
pub enum CloseError {
    /// The transferred engine connection failed to close gracefully.
    Connection(EngineConnectionError),
    /// The SDK-owned shutdown task was abandoned before completion.
    Interrupted,
    /// An injected close implementation unwound.
    Panicked,
}

impl fmt::Display for CloseError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Connection(_) => "the engine connection could not be closed",
            Self::Interrupted => "client close was interrupted",
            Self::Panicked => "the engine connection panicked while closing",
        })
    }
}

impl fmt::Debug for CloseError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Connection(error) => formatter.debug_tuple("Connection").field(error).finish(),
            Self::Interrupted => formatter.write_str("Interrupted"),
            Self::Panicked => formatter.write_str("Panicked"),
        }
    }
}

impl Error for CloseError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Connection(error) => Some(error),
            Self::Interrupted | Self::Panicked => None,
        }
    }
}

/// Transitional generated-binding error retained until query execution moves onto
/// the shared-session facade.
pub enum DaggerError {
    /// Query construction failed.
    Build(QueryBuildError),
    /// A generated input could not be serialized.
    Serialize(RequestEncodingError),
    /// The beta GraphQL adapter failed.
    Query(crate::core::graphql_client::GraphQLError),
    /// Selected data could not be unpacked.
    Unpack(DaggerUnpackError),
    /// The beta CLI downloader failed.
    DownloadClient(EngineConnectionError),
}

impl fmt::Display for DaggerError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Build(_) => "failed to build the internal GraphQL query",
            Self::Serialize(_) => "failed to encode a GraphQL input",
            Self::Query(_) => "failed to query the Dagger engine",
            Self::Unpack(_) => "failed to unpack the GraphQL response",
            Self::DownloadClient(_) => "failed to acquire the Dagger CLI",
        })
    }
}

impl fmt::Debug for DaggerError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Build(_) => "DaggerError::Build",
            Self::Serialize(_) => "DaggerError::Serialize",
            Self::Query(_) => "DaggerError::Query",
            Self::Unpack(_) => "DaggerError::Unpack",
            Self::DownloadClient(_) => "DaggerError::DownloadClient",
        })
    }
}

impl Error for DaggerError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Build(error) => Some(error),
            Self::Serialize(error) => Some(error),
            Self::Query(error) => Some(error),
            Self::Unpack(error) => Some(error),
            Self::DownloadClient(error) => Some(error),
        }
    }
}

/// Transitional selected-data decoding error.
pub enum DaggerUnpackError {
    /// More object layers were present than the beta selector could traverse.
    TooManyNestedObjects,
    /// JSON selected data could not be decoded.
    Deserialize(ResponseDecodingError),
}

impl fmt::Display for DaggerUnpackError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::TooManyNestedObjects => "the GraphQL response is nested too deeply",
            Self::Deserialize(_) => "the selected GraphQL response could not be decoded",
        })
    }
}

impl fmt::Debug for DaggerUnpackError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::TooManyNestedObjects => "TooManyNestedObjects",
            Self::Deserialize(_) => "Deserialize",
        })
    }
}

impl Error for DaggerUnpackError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Deserialize(error) => Some(error),
            Self::TooManyNestedObjects => None,
        }
    }
}

#[derive(Debug)]
struct EyreReportSource(eyre::Report);

impl fmt::Display for EyreReportSource {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("a legacy internal operation failed")
    }
}

impl Error for EyreReportSource {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(self.0.as_ref())
    }
}

impl ConnectError {
    pub(crate) fn from_legacy_connection(error: eyre::Report) -> Self {
        Self::Connection(EngineConnectionError::with_source(
            EngineConnectionErrorKind::Other,
            EyreReportSource(error),
        ))
    }

    pub(crate) fn from_legacy_close(error: eyre::Report) -> Self {
        Self::Close(CloseError::Connection(EngineConnectionError::with_source(
            EngineConnectionErrorKind::Other,
            EyreReportSource(error),
        )))
    }
}

impl DaggerError {
    pub(crate) fn from_legacy_download(error: eyre::Report) -> Self {
        Self::DownloadClient(EngineConnectionError::with_source(
            EngineConnectionErrorKind::Other,
            EyreReportSource(error),
        ))
    }
}

impl From<serde_json::Error> for ResponseDecodingError {
    fn from(error: serde_json::Error) -> Self {
        Self::with_source(ResponseDecodingErrorKind::Json, error)
    }
}
