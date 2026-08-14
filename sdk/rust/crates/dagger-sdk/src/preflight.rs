//! Side-effect-bounded validation and deterministic connection planning.
//!
//! This module is the boundary between the pure [`crate::ClientConfig`] builder and
//! the concrete connector. It may inspect current filesystem state and capture one
//! process snapshot, but it cannot discover a CLI, access the network, spawn a process,
//! or open a connection.
//! Every rejected configuration therefore fails before external work is representable.

use std::ffi::{OsStr, OsString};
use std::fmt;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use crate::config::{ClientConfig, ClientConfigParts, ConfigExplicitness};
use crate::connection::EngineConnection;
use crate::diagnostic::{DiagnosticDispatcher, DiagnosticSink};
use crate::discovery::NativeDiscoveryInputs;
use crate::errors::{ConfigError, ConfigOption, ConnectError};
use crate::session::ExistingSessionInput;
#[cfg(feature = "signoff-observation")]
use crate::signoff_observation::{SignoffConnectorEvent, SignoffObservationDispatcher};
use crate::target::{ArchiveDescriptor, exact_target};

const SESSION_PORT_KEY: &str = "DAGGER_SESSION_PORT";
const SESSION_TOKEN_KEY: &str = "DAGGER_SESSION_TOKEN";
const EXPLICIT_CLI_KEY: &str = "_EXPERIMENTAL_DAGGER_CLI_BIN";
const RUNNER_HOST_KEY: &str = "_EXPERIMENTAL_DAGGER_RUNNER_HOST";
const RUNNER_TOKEN_KEY: &str = "_EXPERIMENTAL_DAGGER_RUNNER_TOKEN";
const TRACEPARENT_KEY: &str = "TRACEPARENT";
const TRACESTATE_KEY: &str = "TRACESTATE";
const BAGGAGE_KEY: &str = "BAGGAGE";

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
    session_port: Option<OsString>,
    session_token: Option<OsString>,
    explicit_cli: Option<OsString>,
    runner: RunnerInputs,
    discovery: NativeDiscoveryInputs,
    propagation: PropagationEnvironment,
}

impl ProcessInputs {
    #[cfg(test)]
    pub(crate) fn no_existing_session() -> Self {
        Self::for_test(None, None, None)
    }

    #[cfg(test)]
    pub(crate) fn existing_session(port: impl Into<OsString>, token: Option<OsString>) -> Self {
        Self::for_test(Some(port.into()), token, None)
    }

    #[cfg(test)]
    pub(crate) fn explicit_cli(value: impl Into<OsString>) -> Self {
        Self::for_test(None, None, Some(value.into()))
    }

    #[cfg(test)]
    pub(crate) fn for_test(
        session_port: Option<OsString>,
        session_token: Option<OsString>,
        explicit_cli: Option<OsString>,
    ) -> Self {
        Self {
            session_port,
            session_token,
            explicit_cli,
            runner: RunnerInputs::default(),
            discovery: NativeDiscoveryInputs::new(
                crate::discovery::NativePathSemantics::current(),
                Vec::new(),
                None,
                None,
                Ok(std::path::PathBuf::from("/virtual/current")),
            ),
            propagation: PropagationEnvironment::default(),
        }
    }

    #[cfg(test)]
    pub(crate) fn with_ambient_for_test(
        mut self,
        runner_host: Option<OsString>,
        runner_token: Option<OsString>,
        traceparent: Option<OsString>,
        tracestate: Option<OsString>,
        baggage: Option<OsString>,
    ) -> Self {
        self.runner = RunnerInputs {
            host: runner_host,
            token: runner_token,
        };
        self.propagation = PropagationEnvironment {
            traceparent,
            tracestate,
            baggage,
        };
        self
    }

    fn capture() -> Self {
        Self {
            session_port: std::env::var_os(SESSION_PORT_KEY),
            session_token: std::env::var_os(SESSION_TOKEN_KEY),
            explicit_cli: std::env::var_os(EXPLICIT_CLI_KEY),
            runner: RunnerInputs {
                host: std::env::var_os(RUNNER_HOST_KEY),
                token: std::env::var_os(RUNNER_TOKEN_KEY),
            },
            discovery: NativeDiscoveryInputs::capture(),
            propagation: PropagationEnvironment {
                traceparent: std::env::var_os(TRACEPARENT_KEY),
                tracestate: std::env::var_os(TRACESTATE_KEY),
                baggage: std::env::var_os(BAGGAGE_KEY),
            },
        }
    }
}

impl fmt::Debug for ProcessInputs {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProcessInputs")
            .field("session_port_present", &self.session_port.is_some())
            .field("session_token_present", &self.session_token.is_some())
            .field("explicit_cli_present", &self.explicit_cli.is_some())
            .field("runner", &self.runner)
            .field("discovery", &self.discovery)
            .field("propagation", &self.propagation)
            .finish()
    }
}

/// Captured runner inputs retained for the later launch projection.
#[derive(Clone, Default, Eq, PartialEq)]
pub(crate) struct RunnerInputs {
    host: Option<OsString>,
    token: Option<OsString>,
}

impl RunnerInputs {
    pub(crate) fn host(&self) -> Option<&OsStr> {
        self.host.as_deref()
    }

    pub(crate) fn token(&self) -> Option<&OsStr> {
        self.token.as_deref()
    }
}

impl fmt::Debug for RunnerInputs {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("RunnerInputs")
            .field("host_present", &self.host.is_some())
            .field("token_present", &self.token.is_some())
            .finish()
    }
}

/// Captured inherited W3C carrier values retained without rendering their content.
#[derive(Clone, Default, Eq, PartialEq)]
pub(crate) struct PropagationEnvironment {
    traceparent: Option<OsString>,
    tracestate: Option<OsString>,
    baggage: Option<OsString>,
}

impl PropagationEnvironment {
    pub(crate) fn values(&self) -> [(&'static str, Option<&OsStr>); 3] {
        [
            (TRACEPARENT_KEY, self.traceparent.as_deref()),
            (TRACESTATE_KEY, self.tracestate.as_deref()),
            (BAGGAGE_KEY, self.baggage.as_deref()),
        ]
    }

    #[cfg(test)]
    pub(crate) fn for_test(
        traceparent: Option<OsString>,
        tracestate: Option<OsString>,
        baggage: Option<OsString>,
    ) -> Self {
        Self {
            traceparent,
            tracestate,
            baggage,
        }
    }
}

impl fmt::Debug for PropagationEnvironment {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("PropagationEnvironment")
            .field("traceparent_present", &self.traceparent.is_some())
            .field("tracestate_present", &self.tracestate.is_some())
            .field("baggage_present", &self.baggage.is_some())
            .finish()
    }
}

/// Captured ambient inputs needed only by a selected CLI source.
#[derive(Clone, Eq, PartialEq)]
pub(crate) struct CliAmbientInputs {
    runner: RunnerInputs,
    propagation: PropagationEnvironment,
}

impl CliAmbientInputs {
    pub(crate) fn runner(&self) -> &RunnerInputs {
        &self.runner
    }

    pub(crate) fn propagation(&self) -> &PropagationEnvironment {
        &self.propagation
    }
}

impl fmt::Debug for CliAmbientInputs {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("CliAmbientInputs")
            .field("runner", &self.runner)
            .field("propagation", &self.propagation)
            .finish()
    }
}

/// Structurally valid configuration whose implicit workdir has passed preflight.
struct ValidatedConfig {
    workdir: Option<std::path::PathBuf>,
    workspace: Option<String>,
    diagnostic_sink: Option<Arc<dyn DiagnosticSink>>,
    #[cfg(feature = "signoff-observation")]
    signoff: SignoffObservationDispatcher,
    load_workspace_modules: bool,
    version: Option<String>,
    verbosity: u8,
    runner_host: Option<String>,
    environment: Vec<(OsString, OsString)>,
    session_startup_timeout: Duration,
    http_connect_timeout: Duration,
    graphql_execution_timeout: Option<Duration>,
    allow_unverified_compatibility: bool,
    explicit: ConfigExplicitness,
}

impl ValidatedConfig {
    fn from_parts(parts: ClientConfigParts) -> Self {
        Self {
            workdir: parts.workdir,
            workspace: parts.workspace,
            diagnostic_sink: parts.diagnostic_sink,
            #[cfg(feature = "signoff-observation")]
            signoff: SignoffObservationDispatcher::new(parts.signoff_recorder),
            load_workspace_modules: parts.load_workspace_modules,
            version: parts.version,
            verbosity: parts.verbosity,
            runner_host: parts.runner_host,
            environment: parts.environment,
            session_startup_timeout: parts.session_startup_timeout,
            http_connect_timeout: parts.http_connect_timeout,
            graphql_execution_timeout: parts.graphql_execution_timeout,
            allow_unverified_compatibility: parts.allow_unverified_compatibility,
            explicit: parts.explicit,
        }
    }

    fn into_plan(self, inputs: ProcessInputs) -> Result<ConnectionPlan, ConnectError> {
        let ProcessInputs {
            session_port,
            session_token,
            explicit_cli,
            runner,
            discovery,
            propagation,
        } = inputs;
        if let Some(port) = session_port {
            self.validate_existing_compatibility()?;
            return Ok(ConnectionPlan::Existing {
                input: ExistingSessionInput {
                    port,
                    token: session_token,
                },
                request: self.into_existing_request(propagation),
            });
        }

        let source = match explicit_cli {
            Some(configured) => CliSourcePlan::ExplicitLocal {
                configured,
                discovery,
            },
            None => CliSourcePlan::CompiledRelease {
                descriptor: ArchiveDescriptor::for_native_target(exact_target()?)?,
                discovery,
            },
        };
        #[cfg(feature = "signoff-observation")]
        if matches!(source, CliSourcePlan::CompiledRelease { .. }) {
            self.signoff
                .emit(SignoffConnectorEvent::CompiledReleaseSelected);
        }
        Ok(ConnectionPlan::NewCli {
            source: Box::new(source),
            request: self.into_cli_launch_request(CliAmbientInputs {
                runner,
                propagation,
            }),
        })
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

    fn into_existing_request(
        self,
        propagation: PropagationEnvironment,
    ) -> ExistingConnectionRequest {
        ExistingConnectionRequest {
            diagnostics: Arc::new(DiagnosticDispatcher::new(self.diagnostic_sink)),
            propagation,
            allow_unverified_compatibility: self.allow_unverified_compatibility,
            session_startup_timeout: self.session_startup_timeout,
            http_connect_timeout: self.http_connect_timeout,
            graphql_execution_timeout: self.graphql_execution_timeout,
        }
    }

    fn into_cli_launch_request(self, ambient: CliAmbientInputs) -> CliLaunchRequest {
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
            ambient,
            diagnostics: Arc::new(DiagnosticDispatcher::new(self.diagnostic_sink)),
            #[cfg(feature = "signoff-observation")]
            signoff: self.signoff,
            allow_unverified_compatibility: self.allow_unverified_compatibility,
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
        input: ExistingSessionInput,
        request: ExistingConnectionRequest,
    },
    NewCli {
        source: Box<CliSourcePlan>,
        request: CliLaunchRequest,
    },
}

/// The authoritative CLI source retained for the complete connection attempt.
pub(crate) enum CliSourcePlan {
    ExplicitLocal {
        configured: OsString,
        discovery: NativeDiscoveryInputs,
    },
    CompiledRelease {
        descriptor: ArchiveDescriptor,
        discovery: NativeDiscoveryInputs,
    },
}

pub(crate) struct ExistingConnectionRequest {
    pub(crate) diagnostics: Arc<DiagnosticDispatcher>,
    pub(crate) propagation: PropagationEnvironment,
    pub(crate) allow_unverified_compatibility: bool,
    pub(crate) session_startup_timeout: Duration,
    pub(crate) http_connect_timeout: Duration,
    pub(crate) graphql_execution_timeout: Option<Duration>,
}

/// Canonical native CLI arguments and managed environment additions.
pub(crate) struct CliLaunchRequest {
    arguments: Vec<OsString>,
    environment: Vec<(OsString, OsString)>,
    ambient: CliAmbientInputs,
    pub(crate) diagnostics: Arc<DiagnosticDispatcher>,
    #[cfg(feature = "signoff-observation")]
    pub(crate) signoff: SignoffObservationDispatcher,
    pub(crate) allow_unverified_compatibility: bool,
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

    pub(crate) fn ambient(&self) -> &CliAmbientInputs {
        &self.ambient
    }

    #[cfg(feature = "signoff-observation")]
    pub(crate) fn signoff(&self) -> &SignoffObservationDispatcher {
        &self.signoff
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
