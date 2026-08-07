//! Private boundary between deterministic connection plans and concrete resources.
//!
//! Explicit connections never cross this boundary. Implicit plans are handed to one
//! connector future under the session-startup deadline; any process resources created
//! by a connector remain armed until a complete connection is ready for transfer.

use std::ffi::OsStr;
use std::io;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use tokio::process::{Child, ChildStdin, Command};
use tokio::task::JoinHandle;

use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
use crate::errors::{ConnectError, RequestError};
use crate::graphql::{RawRequest, RawResponse};
use crate::preflight::{
    CliLaunchRequest, ConnectionPlan, ExistingConnectionRequest, ExistingSessionParams,
};

pub(crate) enum ConnectionRequest {
    Existing {
        params: ExistingSessionParams,
        request: ExistingConnectionRequest,
    },
    NewCli {
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
            Self::NewCli { request } => (
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
            ConnectionPlan::Existing { params, request } => Ok(Self::Existing { params, request }),
            ConnectionPlan::NewCli { request } => Ok(Self::NewCli { request }),
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
            ConnectionRequest::Existing { params, request } => {
                ExistingSessionConnection::connect(params, request.http_connect_timeout)
                    .map(|connection| Box::new(connection) as Box<dyn EngineConnection>)
            }
            ConnectionRequest::NewCli { request } => {
                // CLI discovery, download, and launch belong to the concrete connector
                // milestone. Constructing the guard here keeps the ownership boundary
                // in place without silently routing the stable facade through the beta
                // callback implementation.
                let pending = PendingConnection::new();
                pending.cleanup().await;
                let _ = request;
                Err(ConnectError::Connection(EngineConnectionError::new(
                    EngineConnectionErrorKind::Unavailable,
                )))
            }
        }
    }
}

struct ExistingSessionConnection {
    client: reqwest::Client,
    endpoint: String,
    token: String,
}

impl ExistingSessionConnection {
    fn connect(
        params: ExistingSessionParams,
        http_connect_timeout: Duration,
    ) -> Result<Self, ConnectError> {
        let port = native_text(&params.port).ok_or_else(protocol_connect_error)?;
        let port = port.parse::<u16>().map_err(|_| protocol_connect_error())?;
        let token = params
            .token
            .as_deref()
            .and_then(native_text)
            .filter(|token| !token.is_empty())
            .ok_or_else(protocol_connect_error)?
            .to_owned();
        let client = reqwest::Client::builder()
            .connect_timeout(http_connect_timeout)
            .build()
            .map_err(|error| {
                ConnectError::Connection(EngineConnectionError::with_source(
                    EngineConnectionErrorKind::Transport,
                    error,
                ))
            })?;

        Ok(Self {
            client,
            endpoint: format!("http://127.0.0.1:{port}/query"),
            token,
        })
    }
}

#[async_trait]
impl EngineConnection for ExistingSessionConnection {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        let body = request.encode_wire().map_err(|error| {
            EngineConnectionError::with_source(EngineConnectionErrorKind::Protocol, error)
        })?;
        let response = self
            .client
            .post(&self.endpoint)
            .basic_auth(&self.token, Some(""))
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .body(body)
            .send()
            .await
            .and_then(reqwest::Response::error_for_status)
            .map_err(|error| {
                let kind = if error.is_connect() && error.is_timeout() {
                    EngineConnectionErrorKind::ConnectTimeout
                } else {
                    EngineConnectionErrorKind::Transport
                };
                EngineConnectionError::with_source(kind, error)
            })?;
        let bytes = response.bytes().await.map_err(|error| {
            EngineConnectionError::with_source(EngineConnectionErrorKind::Transport, error)
        })?;
        RawResponse::decode_wire(&bytes).map_err(|error| {
            EngineConnectionError::with_source(EngineConnectionErrorKind::Protocol, error)
        })
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        // An environment-selected session is externally owned. Dropping this SDK's
        // transport state is sufficient; signalling the engine would violate the
        // ownership boundary shared with sibling clients.
        Ok(())
    }

    fn abort(&self) {
        // The external engine is deliberately untouched for the same reason as close.
    }
}

fn native_text(value: &OsStr) -> Option<&str> {
    value.to_str()
}

fn protocol_connect_error() -> ConnectError {
    ConnectError::Connection(EngineConnectionError::new(
        EngineConnectionErrorKind::Protocol,
    ))
}

/// Armed owner for process resources created before connection establishment finishes.
// The concrete CLI connector consumes the remaining methods in its milestone. Keeping
// them compiled now makes its cancellation contract reviewable alongside the owned
// facade which already depends on the guard's cleanup behavior.
#[cfg_attr(not(test), allow(dead_code))]
pub(crate) struct PendingConnection {
    child: Option<Child>,
    stdin: Option<ChildStdin>,
    io_tasks: Vec<JoinHandle<()>>,
    armed: bool,
}

#[cfg_attr(not(test), allow(dead_code))]
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
#[cfg_attr(not(test), allow(dead_code))]
pub(crate) struct PendingResources {
    pub(crate) child: Option<Child>,
    pub(crate) stdin: Option<ChildStdin>,
    pub(crate) io_tasks: Vec<JoinHandle<()>>,
}

#[cfg_attr(not(test), allow(dead_code))]
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
#[cfg_attr(not(test), allow(dead_code))]
pub(crate) struct CliSessionConnection {
    transport: Arc<dyn EngineConnection>,
    resources: Mutex<Option<PendingResources>>,
}

#[cfg_attr(not(test), allow(dead_code))]
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
