//! Private boundary between deterministic connection plans and concrete resources.
//!
//! Explicit connections never cross this boundary. Implicit plans are handed to one
//! connector future under the session-startup deadline; any process resources created
//! by a connector remain armed until a complete connection is ready for transfer.

#[cfg(test)]
use std::io;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
#[cfg(test)]
use tokio::process::{Child, ChildStdin, Command};
#[cfg(test)]
use tokio::task::JoinHandle;

use crate::compatibility::CompatibilityValidator;
use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
use crate::discovery::{LaunchExecutable, NativeDiscoveryInputs, resolve_explicit_cli};
use crate::errors::{ConnectError, RequestError};
use crate::graphql::{RawRequest, RawResponse};
use crate::launch::{
    CliSelectionError, CliSessionStart, NativeCompatibilityResolver, SelectedCli,
    TokioProcessSpawner, TokioRetryClock, select_compiled_cli,
};
use crate::preflight::{
    CliLaunchRequest, CliSourcePlan, ConnectionPlan, ExistingConnectionRequest,
};
use crate::provision::{DefaultCliProvisioner, ReqwestProvisioningHttp};
use crate::provisioning_control::ProvisioningCancellation;
use crate::runtime_errors::{
    ProvisioningError, ProvisioningErrorKind, SessionStartupError, SessionStartupErrorKind,
};
use crate::session::{ExistingSessionInput, validate_existing_session};
use crate::session_startup::{SessionLauncher, SessionResources, SessionStartError};
use crate::target::ArchiveDescriptor;
use crate::transport::{LoopbackTransportFactory, ReqwestLoopbackFactory};

pub(crate) enum ConnectionRequest {
    Existing {
        input: ExistingSessionInput,
        request: ExistingConnectionRequest,
    },
    NewCli {
        source: Box<CliSourcePlan>,
        request: CliLaunchRequest,
    },
}

impl ConnectionRequest {
    pub(crate) fn timeouts(&self) -> (Duration, Duration, Option<Duration>) {
        match self {
            Self::Existing { request, .. } => (
                request.session_startup_timeout,
                request.http_connect_timeout,
                request.graphql_execution_timeout,
            ),
            Self::NewCli { request, .. } => (
                request.session_startup_timeout,
                request.http_connect_timeout,
                request.graphql_execution_timeout,
            ),
        }
    }
}

impl TryFrom<ConnectionPlan> for ConnectionRequest {
    type Error = ConnectionPlan;

    fn try_from(plan: ConnectionPlan) -> Result<Self, Self::Error> {
        match plan {
            ConnectionPlan::Existing { input, request } => Ok(Self::Existing { input, request }),
            ConnectionPlan::NewCli { source, request } => Ok(Self::NewCli { source, request }),
            explicit @ ConnectionPlan::Explicit { .. } => Err(explicit),
        }
    }
}

#[async_trait]
pub(crate) trait Connector: Send + Sync {
    async fn connect(
        &self,
        request: ConnectionRequest,
    ) -> Result<Box<dyn EngineConnection>, ConnectError>;
}

pub(crate) struct DefaultConnector;

#[async_trait]
impl Connector for DefaultConnector {
    async fn connect(
        &self,
        request: ConnectionRequest,
    ) -> Result<Box<dyn EngineConnection>, ConnectError> {
        match request {
            ConnectionRequest::Existing { input, request } => {
                let session = validate_existing_session(input)?;
                let connection = ReqwestLoopbackFactory
                    .loopback(
                        session.port(),
                        session.token().clone(),
                        request.http_connect_timeout,
                        request.propagation,
                        Some(session.resource()),
                    )
                    .map_err(ConnectError::Connection)?;
                let validator = CompatibilityValidator::exact()?;
                validator
                    .validate(&connection, request.allow_unverified_compatibility)
                    .await
                    .map_err(ConnectError::Compatibility)?;
                Ok(Box::new(connection))
            }
            ConnectionRequest::NewCli { source, request } => connect_new_cli(source, request).await,
        }
    }
}

async fn connect_new_cli(
    source: Box<CliSourcePlan>,
    request: CliLaunchRequest,
) -> Result<Box<dyn EngineConnection>, ConnectError> {
    let prepared = PreparedCliSource::from_plan(source)?;
    #[cfg(test)]
    let compiled_source = matches!(&prepared, PreparedCliSource::CompiledRelease { .. });
    let cancellation = ProvisioningCancellation::new();
    let selected = match prepared {
        PreparedCliSource::ExplicitLocal(executable) => SelectedCli::explicit(executable),
        PreparedCliSource::CompiledRelease {
            descriptor,
            discovery,
        } => {
            let http = ReqwestProvisioningHttp::new()
                .map_err(|error| ConnectError::Provisioning(ProvisioningError::from(error)))?;
            let provisioner = DefaultCliProvisioner::native(http)
                .map_err(|error| ConnectError::Provisioning(ProvisioningError::from(error)))?;
            select_compiled_cli(
                &provisioner,
                &NativeCompatibilityResolver,
                &descriptor,
                &discovery,
                &cancellation,
            )
            .await
            .map_err(map_selection_error)?
        }
    };

    let inherited = request.ambient().propagation().clone();
    let connect_timeout = request.http_connect_timeout;
    let allow_unverified = request.allow_unverified_compatibility;
    let start = CliSessionStart::new(selected, request);
    #[cfg(test)]
    {
        let projection = start.projection();
        let propagation = projection.environment().iter().any(|(key, _)| {
            key.to_str().is_some_and(|key| {
                key.eq_ignore_ascii_case("TRACEPARENT") || key.eq_ignore_ascii_case("BAGGAGE")
            })
        });
        crate::session_startup::observe_live_connector(
            compiled_source,
            propagation,
            start.options().diagnostics.is_enabled(),
        );
    }
    let mut launcher = SessionLauncher::new(TokioProcessSpawner, TokioRetryClock);
    let started = launcher
        .launch(start, &cancellation)
        .await
        .map_err(map_start_error)?;
    let transport = match ReqwestLoopbackFactory.loopback(
        started.parameters().port().get(),
        started.parameters().token().clone(),
        connect_timeout,
        inherited,
        None,
    ) {
        Ok(transport) => transport,
        Err(error) => {
            started.rollback().await;
            return Err(ConnectError::Connection(error));
        }
    };
    let validator = CompatibilityValidator::exact()?;
    if let Err(error) = validator.validate(&transport, allow_unverified).await {
        started.rollback().await;
        return Err(ConnectError::Compatibility(error));
    }
    let (_, resources) = started.transfer();
    Ok(Box::new(OwnedCliConnection::new(transport, resources)))
}

fn map_selection_error(error: CliSelectionError) -> ConnectError {
    match error {
        CliSelectionError::Provision(error) => {
            ConnectError::Provisioning(ProvisioningError::from(error))
        }
        compound @ CliSelectionError::Compatibility { .. } => {
            let status = compound
                .release_cause()
                .and_then(crate::provisioning_error::ProvisionError::status);
            ConnectError::Provisioning(ProvisioningError::new(
                ProvisioningErrorKind::CompatibilityFallback,
                status,
                Some(Arc::new(compound)),
            ))
        }
    }
}

fn map_start_error(error: SessionStartError) -> ConnectError {
    let kind = match &error {
        SessionStartError::Spawn(_) => SessionStartupErrorKind::Spawn,
        SessionStartError::Protocol { .. } => SessionStartupErrorKind::ControlProtocol,
    };
    ConnectError::SessionStartup(SessionStartupError::with_source(kind, error))
}

struct OwnedCliConnection<T> {
    transport: T,
    resources: Mutex<Option<SessionResources>>,
}

impl<T> OwnedCliConnection<T> {
    fn new(transport: T, resources: SessionResources) -> Self {
        Self {
            transport,
            resources: Mutex::new(Some(resources)),
        }
    }

    fn take_resources(&self) -> Option<SessionResources> {
        match self.resources.lock() {
            Ok(mut resources) => resources.take(),
            Err(poisoned) => poisoned.into_inner().take(),
        }
    }
}

#[async_trait]
impl<T> EngineConnection for OwnedCliConnection<T>
where
    T: EngineConnection,
{
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.transport.execute(request).await
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        let transport = self.transport.close().await;
        let resources = match self.take_resources() {
            Some(resources) => resources.close().await.map(|_| ()),
            None => Ok(()),
        };
        transport.and(resources)
    }

    fn abort(&self) {
        // Dropping the single resource bundle starts termination before the transport
        // backstop and never waits in the caller's destructor.
        drop(self.take_resources());
        self.transport.abort();
    }
}

#[allow(dead_code)]
enum PreparedCliSource {
    ExplicitLocal(LaunchExecutable),
    CompiledRelease {
        descriptor: Box<ArchiveDescriptor>,
        discovery: Box<NativeDiscoveryInputs>,
    },
}

impl PreparedCliSource {
    fn from_plan(plan: Box<CliSourcePlan>) -> Result<Self, ConnectError> {
        match *plan {
            CliSourcePlan::ExplicitLocal {
                configured,
                discovery,
            } => Ok(Self::ExplicitLocal(resolve_explicit_cli(
                configured, &discovery,
            )?)),
            CliSourcePlan::CompiledRelease {
                descriptor,
                discovery,
            } => Ok(Self::CompiledRelease {
                descriptor: Box::new(descriptor),
                discovery: Box::new(discovery),
            }),
        }
    }
}

/// Test guard for partially acquired process resources at cancellation boundaries.
#[cfg(test)]
pub(crate) struct PendingConnection {
    child: Option<Child>,
    stdin: Option<ChildStdin>,
    io_tasks: Vec<JoinHandle<()>>,
    armed: bool,
}

#[cfg(test)]
impl PendingConnection {
    pub(crate) fn new() -> Self {
        Self {
            child: None,
            stdin: None,
            io_tasks: Vec::new(),
            armed: true,
        }
    }

    pub(crate) fn spawn_child(&mut self, command: &mut Command) -> Result<(), io::Error> {
        // kill_on_drop is the no-runtime backstop; normal paths still reap explicitly
        // so operating-system process state never escapes the guard.
        command.kill_on_drop(true);
        let mut child = command.spawn()?;
        self.stdin = child.stdin.take();
        self.child = Some(child);
        Ok(())
    }

    pub(crate) fn push_io_task(&mut self, task: JoinHandle<()>) {
        self.io_tasks.push(task);
    }

    pub(crate) async fn cleanup(mut self) {
        self.cleanup_resources().await;
        self.armed = false;
    }

    pub(crate) fn disarm(mut self) -> PendingResources {
        self.armed = false;
        PendingResources {
            child: self.child.take(),
            stdin: self.stdin.take(),
            io_tasks: std::mem::take(&mut self.io_tasks),
        }
    }

    async fn cleanup_resources(&mut self) {
        self.stdin.take();
        if let Some(mut child) = self.child.take() {
            let _ = child.start_kill();
            let _ = child.wait().await;
        }
        for task in self.io_tasks.drain(..) {
            task.abort();
            let _ = task.await;
        }
    }
}

#[cfg(test)]
impl Drop for PendingConnection {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }

        let mut child = self.child.take();
        self.stdin.take();
        if let Some(child) = &mut child {
            // Termination begins synchronously. Reaping can then move to an owned task
            // without requiring the cancelled connector future to be polled again.
            let _ = child.start_kill();
        }
        let tasks = std::mem::take(&mut self.io_tasks);
        for task in &tasks {
            task.abort();
        }

        if let Ok(runtime) = tokio::runtime::Handle::try_current() {
            runtime.spawn(async move {
                if let Some(mut child) = child {
                    let _ = child.wait().await;
                }
                for task in tasks {
                    let _ = task.await;
                }
            });
        }
        // Without a runtime, dropping Child invokes kill_on_drop and dropping aborted
        // JoinHandles detaches no continuing work because abort was already requested.
    }
}

/// Process resources transferred only after a complete connection is available.
#[cfg(test)]
pub(crate) struct PendingResources {
    pub(crate) child: Option<Child>,
    pub(crate) stdin: Option<ChildStdin>,
    pub(crate) io_tasks: Vec<JoinHandle<()>>,
}

#[cfg(test)]
impl PendingResources {
    pub(crate) async fn close(mut self) -> Result<(), EngineConnectionError> {
        // Closing stdin is the portable graceful session signal used by the CLI. The
        // child is reaped before either stream drainer is joined, matching Go's owned
        // session contract without sending a signal to externally owned engines.
        self.stdin.take();
        if let Some(mut child) = self.child.take() {
            child.wait().await.map_err(|error| {
                EngineConnectionError::with_source(EngineConnectionErrorKind::Transport, error)
            })?;
        }
        for task in self.io_tasks.drain(..) {
            task.await.map_err(|error| {
                EngineConnectionError::with_source(EngineConnectionErrorKind::Transport, error)
            })?;
        }
        Ok(())
    }
}

#[cfg(test)]
impl Drop for PendingResources {
    fn drop(&mut self) {
        let mut child = self.child.take();
        self.stdin.take();
        if let Some(child) = &mut child {
            let _ = child.start_kill();
        }
        let tasks = std::mem::take(&mut self.io_tasks);
        for task in &tasks {
            task.abort();
        }
        if let Ok(runtime) = tokio::runtime::Handle::try_current() {
            runtime.spawn(async move {
                if let Some(mut child) = child {
                    let _ = child.wait().await;
                }
                for task in tasks {
                    let _ = task.await;
                }
            });
        }
    }
}

/// Complete CLI-owned resource transferred from an armed connection attempt.
///
/// The concrete discovery and launch adapter creates this value only after session
/// parameters and the HTTP transport are ready. It centralizes graceful close and
/// emergency cleanup so generated handles never own process state separately.
#[cfg(test)]
pub(crate) struct CliSessionConnection {
    transport: Arc<dyn EngineConnection>,
    resources: Mutex<Option<PendingResources>>,
}

#[cfg(test)]
impl CliSessionConnection {
    pub(crate) fn new(transport: Box<dyn EngineConnection>, resources: PendingResources) -> Self {
        Self {
            transport: Arc::from(transport),
            resources: Mutex::new(Some(resources)),
        }
    }

    fn take_resources(&self) -> Option<PendingResources> {
        match self.resources.lock() {
            Ok(mut resources) => resources.take(),
            Err(poisoned) => poisoned.into_inner().take(),
        }
    }
}

#[async_trait]
#[cfg(test)]
impl EngineConnection for CliSessionConnection {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.transport.execute(request).await
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        let transport_result = self.transport.close().await;
        let resource_result = match self.take_resources() {
            Some(resources) => resources.close().await,
            None => Ok(()),
        };
        transport_result.and(resource_result)
    }

    fn abort(&self) {
        // PendingResources::drop starts kill synchronously and transfers reaping to an
        // owned runtime task, so this trait method remains prompt and non-blocking.
        drop(self.take_resources());
        // Resource termination begins before calling an injected transport backstop;
        // even a contract-violating panic cannot strand the owned child or I/O tasks.
        self.transport.abort();
    }
}

pub(crate) fn map_connection_error(
    error: EngineConnectionError,
    http_connect_timeout: Option<Duration>,
) -> RequestError {
    if error.kind() == EngineConnectionErrorKind::ConnectTimeout
        && let Some(duration) = http_connect_timeout
    {
        return RequestError::TransportConnectTimeout { duration };
    }
    RequestError::Connection(error)
}
