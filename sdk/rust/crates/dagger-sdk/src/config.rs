//! Pure construction and normalization of the stable client configuration.
//!
//! Building a [`ClientConfig`] performs no filesystem, process-environment, CLI,
//! network, or connector work. It validates only value-local structure and conflicts
//! which are knowable without selecting a connection source. Filesystem and
//! source-dependent checks belong to the later preflight boundary.

use std::ffi::{OsStr, OsString};
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use crate::connection::EngineConnection;
use crate::diagnostic::DiagnosticSink;
use crate::errors::{ConfigError, ConfigOption, TimeoutPhase};

const DEFAULT_SESSION_STARTUP_TIMEOUT: Duration = Duration::from_secs(300);
const DEFAULT_HTTP_CONNECT_TIMEOUT: Duration = Duration::from_secs(10);

const RESERVED_ENVIRONMENT_KEYS: [&[u8]; 7] = [
    b"DAGGER_SESSION_PORT",
    b"DAGGER_SESSION_TOKEN",
    b"_EXPERIMENTAL_DAGGER_CLI_BIN",
    b"_EXPERIMENTAL_DAGGER_RUNNER_HOST",
    b"TRACEPARENT",
    b"TRACESTATE",
    b"BAGGAGE",
];

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub(crate) struct ConfigExplicitness {
    pub(crate) workdir: bool,
    pub(crate) workspace: bool,
    pub(crate) diagnostic_sink: bool,
    pub(crate) load_workspace_modules: bool,
    pub(crate) connection: bool,
    pub(crate) version: bool,
    pub(crate) verbosity: bool,
    pub(crate) runner_host: bool,
    pub(crate) environment: bool,
    pub(crate) session_startup_timeout: bool,
    pub(crate) http_connect_timeout: bool,
    pub(crate) graphql_execution_timeout: bool,
}

/// Immutable, validated inputs used to establish an owned Dagger client.
///
/// The default uses a 300-second session-startup timeout, a 10-second HTTP-connect
/// timeout, no complete GraphQL execution timeout, and no CLI-only options. An
/// explicitly supplied [`EngineConnection`] is consumed with the configuration and is
/// compatible only with the GraphQL execution timeout.
pub struct ClientConfig {
    workdir: Option<PathBuf>,
    workspace: Option<String>,
    diagnostic_sink: Option<Arc<dyn DiagnosticSink>>,
    load_workspace_modules: bool,
    connection: Option<Box<dyn EngineConnection>>,
    version: Option<String>,
    verbosity: u8,
    runner_host: Option<String>,
    environment: Vec<(OsString, OsString)>,
    session_startup_timeout: Duration,
    http_connect_timeout: Duration,
    graphql_execution_timeout: Option<Duration>,
    explicit: ConfigExplicitness,
}

impl ClientConfig {
    /// Starts a consuming builder with every documented default.
    pub fn builder() -> ClientConfigBuilder {
        ClientConfigBuilder::default()
    }

    /// Returns the configured workdir without reading filesystem state.
    pub fn workdir(&self) -> Option<&Path> {
        self.workdir.as_deref()
    }

    /// Returns the local or remote workspace reference.
    pub fn workspace(&self) -> Option<&str> {
        self.workspace.as_deref()
    }

    /// Reports whether a caller-owned diagnostic sink is present.
    pub fn has_diagnostic_sink(&self) -> bool {
        self.diagnostic_sink.is_some()
    }

    /// Returns whether a newly started CLI should load workspace modules.
    pub const fn load_workspace_modules(&self) -> bool {
        self.load_workspace_modules
    }

    /// Reports whether ownership of an explicit connection is stored in this value.
    pub fn has_explicit_connection(&self) -> bool {
        self.connection.is_some()
    }

    /// Returns the advanced engine schema version override.
    ///
    /// This testing facility is part of the stable API but can deliberately request a
    /// schema which does not match the generated bindings.
    pub fn version(&self) -> Option<&str> {
        self.version.as_deref()
    }

    /// Returns the CLI diagnostic verbosity level.
    pub const fn verbosity(&self) -> u8 {
        self.verbosity
    }

    /// Returns the advanced runner-host URI.
    ///
    /// This testing facility is forwarded only when a new CLI session is selected.
    pub fn runner_host(&self) -> Option<&str> {
        self.runner_host.as_deref()
    }

    /// Returns additional environment in caller insertion order.
    pub fn environment(&self) -> impl ExactSizeIterator<Item = (&OsStr, &OsStr)> {
        self.environment
            .iter()
            .map(|(key, value)| (key.as_os_str(), value.as_os_str()))
    }

    /// Returns the bound on establishing a newly selected connection.
    pub const fn session_startup_timeout(&self) -> Duration {
        self.session_startup_timeout
    }

    /// Returns the bound on establishing an SDK-owned HTTP connection.
    pub const fn http_connect_timeout(&self) -> Duration {
        self.http_connect_timeout
    }

    /// Returns the optional bound on one complete GraphQL request.
    pub const fn graphql_execution_timeout(&self) -> Option<Duration> {
        self.graphql_execution_timeout
    }
}

impl Default for ClientConfig {
    fn default() -> Self {
        Self {
            workdir: None,
            workspace: None,
            diagnostic_sink: None,
            load_workspace_modules: false,
            connection: None,
            version: None,
            verbosity: 0,
            runner_host: None,
            environment: Vec::new(),
            session_startup_timeout: DEFAULT_SESSION_STARTUP_TIMEOUT,
            http_connect_timeout: DEFAULT_HTTP_CONNECT_TIMEOUT,
            graphql_execution_timeout: None,
            explicit: ConfigExplicitness::default(),
        }
    }
}

struct EnvironmentKeys<'a>(&'a [(OsString, OsString)]);

impl fmt::Debug for EnvironmentKeys<'_> {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_list()
            .entries(self.0.iter().map(|(key, _)| key))
            .finish()
    }
}

impl fmt::Debug for ClientConfig {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        // Paths, URIs, trait objects, and values are intentionally represented only by
        // presence. They can contain credentials or sensitive host coordinates even
        // when the corresponding option is not conventionally called a secret.
        formatter
            .debug_struct("ClientConfig")
            .field("workdir_present", &self.workdir.is_some())
            .field("workspace_present", &self.workspace.is_some())
            .field("diagnostic_sink_present", &self.diagnostic_sink.is_some())
            .field("load_workspace_modules", &self.load_workspace_modules)
            .field("explicit_connection_present", &self.connection.is_some())
            .field("version_present", &self.version.is_some())
            .field("verbosity", &self.verbosity)
            .field("runner_host_present", &self.runner_host.is_some())
            .field("environment_keys", &EnvironmentKeys(&self.environment))
            .field("session_startup_timeout", &self.session_startup_timeout)
            .field("http_connect_timeout", &self.http_connect_timeout)
            .field(
                "graphql_execution_timeout_present",
                &self.graphql_execution_timeout.is_some(),
            )
            // Explicitness is lifecycle-planning state, not a second set of values;
            // rendering the bitset makes defaults reviewable without exposing inputs.
            .field("explicit_inputs", &self.explicit)
            .finish()
    }
}

/// Consuming builder for [`ClientConfig`].
///
/// Setters merely retain candidate values. [`ClientConfigBuilder::build`] performs all
/// pure normalization and returns one typed error without panicking.
#[derive(Default)]
pub struct ClientConfigBuilder {
    workdir: Option<PathBuf>,
    workspace: Option<String>,
    diagnostic_sink: Option<Arc<dyn DiagnosticSink>>,
    load_workspace_modules: Option<bool>,
    connection: Option<Box<dyn EngineConnection>>,
    version: Option<String>,
    verbosity: Option<u64>,
    runner_host: Option<String>,
    environment: Vec<(OsString, OsString)>,
    session_startup_timeout: Option<Duration>,
    http_connect_timeout: Option<Duration>,
    graphql_execution_timeout: Option<Duration>,
}

impl ClientConfigBuilder {
    /// Sets the optional host workdir. Filesystem existence is checked during preflight.
    #[must_use]
    pub fn workdir(mut self, path: impl Into<PathBuf>) -> Self {
        self.workdir = Some(path.into());
        self
    }

    /// Sets a non-empty local path or remote workspace reference.
    #[must_use]
    pub fn workspace(mut self, workspace: impl Into<String>) -> Self {
        self.workspace = Some(workspace.into());
        self
    }

    /// Sets the optional progress and lifecycle diagnostic destination.
    #[must_use]
    pub fn diagnostic_sink(mut self, sink: Arc<dyn DiagnosticSink>) -> Self {
        self.diagnostic_sink = Some(sink);
        self
    }

    /// Opts a newly started CLI session into workspace-module loading.
    #[must_use]
    pub fn load_workspace_modules(mut self, enabled: bool) -> Self {
        self.load_workspace_modules = Some(enabled);
        self
    }

    /// Transfers ownership of an advanced caller-created connection.
    #[must_use]
    pub fn connection(mut self, connection: Box<dyn EngineConnection>) -> Self {
        self.connection = Some(connection);
        self
    }

    /// Sets an advanced semantic engine schema version override.
    #[must_use]
    pub fn version(mut self, version: impl Into<String>) -> Self {
        self.version = Some(version.into());
        self
    }

    /// Sets CLI diagnostic verbosity, retaining the wide input until validation.
    #[must_use]
    pub fn verbosity(mut self, verbosity: u64) -> Self {
        self.verbosity = Some(verbosity);
        self
    }

    /// Sets an advanced absolute runner-host URI.
    #[must_use]
    pub fn runner_host(mut self, runner_host: impl Into<String>) -> Self {
        self.runner_host = Some(runner_host.into());
        self
    }

    /// Appends one native child-process environment entry.
    #[must_use]
    pub fn environment(mut self, key: impl Into<OsString>, value: impl Into<OsString>) -> Self {
        self.environment.push((key.into(), value.into()));
        self
    }

    /// Sets the positive session-startup bound.
    #[must_use]
    pub fn session_startup_timeout(mut self, timeout: Duration) -> Self {
        self.session_startup_timeout = Some(timeout);
        self
    }

    /// Sets the positive SDK-owned HTTP-connect bound.
    #[must_use]
    pub fn http_connect_timeout(mut self, timeout: Duration) -> Self {
        self.http_connect_timeout = Some(timeout);
        self
    }

    /// Sets the positive optional complete-request bound.
    #[must_use]
    pub fn graphql_execution_timeout(mut self, timeout: Duration) -> Self {
        self.graphql_execution_timeout = Some(timeout);
        self
    }

    /// Validates and normalizes the candidate without performing external work.
    pub fn build(self) -> Result<ClientConfig, ConfigError> {
        validate_workdir(self.workdir.as_deref())?;
        validate_workspace(self.workspace.as_deref())?;
        validate_version(self.version.as_deref())?;
        validate_runner_host(self.runner_host.as_deref())?;
        validate_timeout(self.session_startup_timeout, TimeoutPhase::SessionStartup)?;
        validate_timeout(self.http_connect_timeout, TimeoutPhase::HttpConnect)?;
        validate_timeout(
            self.graphql_execution_timeout,
            TimeoutPhase::GraphQlExecution,
        )?;
        validate_environment(&self.environment)?;

        let verbosity = match self.verbosity {
            Some(value) => u8::try_from(value).map_err(|_| ConfigError::VerbosityOutOfRange)?,
            None => 0,
        };

        if self.connection.is_some() {
            validate_explicit_connection_conflicts(&self)?;
        }

        let explicit = ConfigExplicitness {
            workdir: self.workdir.is_some(),
            workspace: self.workspace.is_some(),
            diagnostic_sink: self.diagnostic_sink.is_some(),
            load_workspace_modules: self.load_workspace_modules.is_some(),
            connection: self.connection.is_some(),
            version: self.version.is_some(),
            verbosity: self.verbosity.is_some(),
            runner_host: self.runner_host.is_some(),
            environment: !self.environment.is_empty(),
            session_startup_timeout: self.session_startup_timeout.is_some(),
            http_connect_timeout: self.http_connect_timeout.is_some(),
            graphql_execution_timeout: self.graphql_execution_timeout.is_some(),
        };

        Ok(ClientConfig {
            workdir: self.workdir,
            workspace: self.workspace,
            diagnostic_sink: self.diagnostic_sink,
            load_workspace_modules: self.load_workspace_modules.unwrap_or(false),
            connection: self.connection,
            version: self.version,
            verbosity,
            runner_host: self.runner_host,
            environment: self.environment,
            session_startup_timeout: self
                .session_startup_timeout
                .unwrap_or(DEFAULT_SESSION_STARTUP_TIMEOUT),
            http_connect_timeout: self
                .http_connect_timeout
                .unwrap_or(DEFAULT_HTTP_CONNECT_TIMEOUT),
            graphql_execution_timeout: self.graphql_execution_timeout,
            explicit,
        })
    }
}

fn validate_workdir(workdir: Option<&Path>) -> Result<(), ConfigError> {
    if workdir.is_some_and(|path| path.as_os_str().is_empty()) {
        return Err(ConfigError::InvalidWorkdir);
    }
    Ok(())
}

fn validate_workspace(workspace: Option<&str>) -> Result<(), ConfigError> {
    if workspace.is_some_and(str::is_empty) {
        return Err(ConfigError::InvalidWorkspace);
    }
    Ok(())
}

fn validate_version(version: Option<&str>) -> Result<(), ConfigError> {
    let Some(version) = version else {
        return Ok(());
    };
    let semantic = version.strip_prefix('v').unwrap_or(version);
    if semantic.is_empty() || semver::Version::parse(semantic).is_err() {
        return Err(ConfigError::InvalidVersion);
    }
    Ok(())
}

fn validate_runner_host(runner_host: Option<&str>) -> Result<(), ConfigError> {
    if runner_host.is_some_and(|value| url::Url::parse(value).is_err()) {
        return Err(ConfigError::InvalidRunnerHost);
    }
    Ok(())
}

fn validate_timeout(timeout: Option<Duration>, phase: TimeoutPhase) -> Result<(), ConfigError> {
    if timeout.is_some_and(|value| value.is_zero()) {
        return Err(ConfigError::InvalidTimeout { phase });
    }
    Ok(())
}

fn validate_environment(environment: &[(OsString, OsString)]) -> Result<(), ConfigError> {
    let mut normalized_keys: Vec<Vec<u8>> = Vec::with_capacity(environment.len());
    for (index, (key, value)) in environment.iter().enumerate() {
        let key_bytes = key.as_os_str().as_encoded_bytes();
        if key_bytes.is_empty() || key_bytes.contains(&b'=') || key_bytes.contains(&0) {
            return Err(ConfigError::InvalidEnvironmentKey { index });
        }
        if value.as_os_str().as_encoded_bytes().contains(&0) {
            return Err(ConfigError::InvalidEnvironmentValue { index });
        }
        if RESERVED_ENVIRONMENT_KEYS
            .iter()
            .any(|reserved| key_bytes.eq_ignore_ascii_case(reserved))
        {
            return Err(ConfigError::ReservedEnvironmentKey { index });
        }

        let normalized = key_bytes.to_ascii_lowercase();
        if let Some(first) = normalized_keys
            .iter()
            .position(|candidate| candidate == &normalized)
        {
            return Err(ConfigError::DuplicateEnvironmentKey {
                first,
                duplicate: index,
            });
        }
        normalized_keys.push(normalized);
    }
    Ok(())
}

fn validate_explicit_connection_conflicts(
    builder: &ClientConfigBuilder,
) -> Result<(), ConfigError> {
    let conflict = if builder.workdir.is_some() {
        Some(ConfigOption::Workdir)
    } else if builder.workspace.is_some() {
        Some(ConfigOption::Workspace)
    } else if builder.diagnostic_sink.is_some() {
        Some(ConfigOption::DiagnosticSink)
    } else if builder.load_workspace_modules.is_some() {
        Some(ConfigOption::LoadWorkspaceModules)
    } else if builder.version.is_some() {
        Some(ConfigOption::Version)
    } else if builder.verbosity.is_some_and(|value| value > 0) {
        Some(ConfigOption::Verbosity)
    } else if builder.runner_host.is_some() {
        Some(ConfigOption::RunnerHost)
    } else if !builder.environment.is_empty() {
        Some(ConfigOption::Environment)
    } else if builder.session_startup_timeout.is_some() {
        Some(ConfigOption::SessionStartupTimeout)
    } else if builder.http_connect_timeout.is_some() {
        Some(ConfigOption::HttpConnectTimeout)
    } else {
        None
    };

    match conflict {
        Some(option) => Err(ConfigError::ExplicitConnectionConflict { option }),
        None => Ok(()),
    }
}
