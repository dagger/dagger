//! Side-effect-bounded validation and deterministic connection planning.
//!
//! This module is the boundary between the pure [`crate::ClientConfig`] builder and
//! the concrete connector. It may inspect current filesystem state and snapshot the two
//! Dagger session environment values needed to select an existing session, but it
//! cannot discover a CLI, access the network, spawn a process, or open a connection.
//! Every rejected configuration therefore fails before external work is representable.

use std::ffi::{OsStr, OsString};
use std::fmt;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use crate::config::{ClientConfig, ClientConfigParts, ConfigExplicitness};
use crate::connection::EngineConnection;
use crate::diagnostic::{DiagnosticDispatcher, DiagnosticSink};
use crate::errors::{ConfigError, ConfigOption, ConnectError};

const SESSION_PORT_KEY: &str = "DAGGER_SESSION_PORT";
const SESSION_TOKEN_KEY: &str = "DAGGER_SESSION_TOKEN";
const RUNNER_HOST_KEY: &str = "_EXPERIMENTAL_DAGGER_RUNNER_HOST";

/// Read-only host observations permitted during preflight.
///
/// Tests replace this seam with a recorder. The absence of discovery, network, spawn,
/// or connection methods is intentional: those effects begin only after a successful
/// [`ConnectionPlan`] reaches the connector.
pub(crate) trait PreflightContext {
    fn is_directory(&self, path: &Path) -> bool;
    fn process_inputs(&self) -> ProcessInputs;
}

pub(crate) struct SystemPreflight;

impl PreflightContext for SystemPreflight {
    fn is_directory(&self, path: &Path) -> bool {
        path.is_dir()
    }

    fn process_inputs(&self) -> ProcessInputs {
        ProcessInputs::capture()
    }
}

/// Immutable snapshot used for one source-selection decision.
#[derive(Clone, Eq, PartialEq)]
pub(crate) struct ProcessInputs {
    existing_session: Option<ExistingSessionParams>,
}

impl ProcessInputs {
    pub(crate) fn no_existing_session() -> Self {
        Self {
            existing_session: None,
        }
    }

    pub(crate) fn existing_session(port: impl Into<OsString>, token: Option<OsString>) -> Self {
        Self {
            existing_session: Some(ExistingSessionParams {
                port: port.into(),
                token,
            }),
        }
    }

    fn capture() -> Self {
        let existing_session =
            std::env::var_os(SESSION_PORT_KEY).map(|port| ExistingSessionParams {
                port,
                token: std::env::var_os(SESSION_TOKEN_KEY),
            });
        Self { existing_session }
    }
}

impl fmt::Debug for ProcessInputs {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProcessInputs")
            .field("existing_session_present", &self.existing_session.is_some())
            .finish()
    }
}

/// Raw existing-session values retained without rendering the credential.
#[derive(Clone, Eq, PartialEq)]
pub(crate) struct ExistingSessionParams {
    pub(crate) port: OsString,
    pub(crate) token: Option<OsString>,
}

impl fmt::Debug for ExistingSessionParams {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ExistingSessionParams")
            .field("port_present", &!self.port.is_empty())
            .field("token_present", &self.token.is_some())
            .finish()
    }
}

/// Structurally valid configuration whose implicit workdir has passed preflight.
struct ValidatedConfig {
    workdir: Option<std::path::PathBuf>,
    workspace: Option<String>,
    diagnostic_sink: Option<Arc<dyn DiagnosticSink>>,
    load_workspace_modules: bool,
    version: Option<String>,
    verbosity: u8,
    runner_host: Option<String>,
    environment: Vec<(OsString, OsString)>,
    session_startup_timeout: Duration,
    http_connect_timeout: Duration,
    graphql_execution_timeout: Option<Duration>,
    explicit: ConfigExplicitness,
}

impl ValidatedConfig {
    fn from_parts(parts: ClientConfigParts) -> Self {
        Self {
            workdir: parts.workdir,
            workspace: parts.workspace,
            diagnostic_sink: parts.diagnostic_sink,
            load_workspace_modules: parts.load_workspace_modules,
            version: parts.version,
            verbosity: parts.verbosity,
            runner_host: parts.runner_host,
            environment: parts.environment,
            session_startup_timeout: parts.session_startup_timeout,
            http_connect_timeout: parts.http_connect_timeout,
            graphql_execution_timeout: parts.graphql_execution_timeout,
            explicit: parts.explicit,
        }
    }

    fn into_plan(self, inputs: ProcessInputs) -> Result<ConnectionPlan, ConnectError> {
        match inputs.existing_session {
            Some(params) => {
                self.validate_existing_compatibility()?;
                Ok(ConnectionPlan::Existing {
                    params,
                    request: self.into_existing_request(),
                })
            }
            None => Ok(ConnectionPlan::NewCli {
                request: self.into_cli_launch_request(),
            }),
        }
    }

    fn validate_existing_compatibility(&self) -> Result<(), ConfigError> {
        if self.workdir.is_some() {
            return Err(ConfigError::ExistingSessionConflict {
                option: ConfigOption::Workdir,
            });
        }
        if self.workspace.is_some() {
            return Err(ConfigError::ExistingSessionConflict {
                option: ConfigOption::Workspace,
            });
        }

        let ineffective = if self.explicit.load_workspace_modules {
            Some(ConfigOption::LoadWorkspaceModules)
        } else if self.version.is_some() {
            Some(ConfigOption::Version)
        } else if self.verbosity > 0 {
            Some(ConfigOption::Verbosity)
        } else if self.runner_host.is_some() {
            Some(ConfigOption::RunnerHost)
        } else if !self.environment.is_empty() {
            Some(ConfigOption::Environment)
        } else {
            None
        };

        match ineffective {
            Some(option) => Err(ConfigError::OptionConflict { option }),
            None => Ok(()),
        }
    }

    fn into_existing_request(self) -> ExistingConnectionRequest {
        ExistingConnectionRequest {
            diagnostics: Arc::new(DiagnosticDispatcher::new(self.diagnostic_sink)),
            session_startup_timeout: self.session_startup_timeout,
            http_connect_timeout: self.http_connect_timeout,
            graphql_execution_timeout: self.graphql_execution_timeout,
        }
    }

    fn into_cli_launch_request(self) -> CliLaunchRequest {
        let mut arguments = vec![OsString::from("session")];
        if let Some(workdir) = self.workdir {
            arguments.push(OsString::from("--workdir"));
            arguments.push(workdir.into_os_string());
        }
        if let Some(workspace) = self.workspace {
            arguments.push(OsString::from("--workspace"));
            arguments.push(OsString::from(workspace));
        }
        if self.load_workspace_modules {
            arguments.push(OsString::from("--load-workspace-modules"));
        }
        if let Some(version) = self.version {
            arguments.push(OsString::from("--version"));
            arguments.push(OsString::from(version));
        }
        if self.verbosity > 0 {
            // One combined flag mirrors the definitive Go projection while preserving
            // the entire validated u8 level; casting or truncating would silently
            // change the caller's selected diagnostic boundary.
            arguments.push(OsString::from(format!(
                "-{}",
                "v".repeat(usize::from(self.verbosity))
            )));
        }

        let mut environment =
            Vec::with_capacity(self.environment.len() + usize::from(self.runner_host.is_some()));
        if let Some(runner_host) = self.runner_host {
            environment.push((OsString::from(RUNNER_HOST_KEY), OsString::from(runner_host)));
        }
        environment.extend(self.environment);

        CliLaunchRequest {
            arguments,
            environment,
            diagnostics: Arc::new(DiagnosticDispatcher::new(self.diagnostic_sink)),
            session_startup_timeout: self.session_startup_timeout,
            http_connect_timeout: self.http_connect_timeout,
            graphql_execution_timeout: self.graphql_execution_timeout,
        }
    }
}

/// Deterministic output of preflight and source compatibility validation.
pub(crate) enum ConnectionPlan {
    Explicit {
        connection: Box<dyn EngineConnection>,
        execution_timeout: Option<Duration>,
    },
    Existing {
        params: ExistingSessionParams,
        request: ExistingConnectionRequest,
    },
    NewCli {
        request: CliLaunchRequest,
    },
}

pub(crate) struct ExistingConnectionRequest {
    pub(crate) diagnostics: Arc<DiagnosticDispatcher>,
    pub(crate) session_startup_timeout: Duration,
    pub(crate) http_connect_timeout: Duration,
    pub(crate) graphql_execution_timeout: Option<Duration>,
}

/// Canonical native CLI arguments and managed environment additions.
pub(crate) struct CliLaunchRequest {
    arguments: Vec<OsString>,
    environment: Vec<(OsString, OsString)>,
    pub(crate) diagnostics: Arc<DiagnosticDispatcher>,
    pub(crate) session_startup_timeout: Duration,
    pub(crate) http_connect_timeout: Duration,
    pub(crate) graphql_execution_timeout: Option<Duration>,
}

impl CliLaunchRequest {
    pub(crate) fn arguments(&self) -> &[OsString] {
        &self.arguments
    }

    pub(crate) fn environment(&self) -> &[(OsString, OsString)] {
        &self.environment
    }

    pub(crate) fn encoded_projection(&self) -> Vec<u8> {
        // Length-prefixing prevents adjacent native values from sharing a byte stream
        // representation, so this is a deterministic comparison aid rather than a
        // shell-escaped launch format.
        let mut encoded = Vec::new();
        for argument in &self.arguments {
            append_native(&mut encoded, argument.as_os_str());
        }
        for (key, value) in &self.environment {
            append_native(&mut encoded, key.as_os_str());
            append_native(&mut encoded, value.as_os_str());
        }
        encoded
    }
}

fn append_native(encoded: &mut Vec<u8>, value: &OsStr) {
    let bytes = value.as_encoded_bytes();
    encoded.extend_from_slice(&bytes.len().to_le_bytes());
    encoded.extend_from_slice(bytes);
}

/// Runs preflight against the real process and filesystem observations.
pub(crate) fn preflight(config: ClientConfig) -> Result<ConnectionPlan, ConnectError> {
    preflight_with(config, &SystemPreflight)
}

/// Runs preflight through a replaceable, read-only observation seam.
pub(crate) fn preflight_with(
    config: ClientConfig,
    context: &impl PreflightContext,
) -> Result<ConnectionPlan, ConnectError> {
    let mut parts = config.into_parts();

    // Explicit injection is checked before either permitted host observation. The
    // builder has already rejected every incompatible explicit input, so moving the
    // box here is the single resource-transfer point.
    if let Some(connection) = parts.connection.take() {
        return Ok(ConnectionPlan::Explicit {
            connection,
            execution_timeout: parts.graphql_execution_timeout,
        });
    }

    if parts
        .workdir
        .as_deref()
        .is_some_and(|workdir| !context.is_directory(workdir))
    {
        return Err(ConfigError::InvalidWorkdir.into());
    }

    let inputs = context.process_inputs();
    ValidatedConfig::from_parts(parts).into_plan(inputs)
}
