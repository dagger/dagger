//! Isolated CLI control parsing and single-owner process startup resources.
//!
//! The first stdout record is consumed into session parameters under a 64 KiB bound;
//! those bytes never enter the diagnostic router. From successful spawn until explicit
//! transfer, one armed owner contains the child, all pipes, the selected executable
//! lease, and every worker handle. Failure and cancellation therefore share one cleanup
//! path instead of relying on detached Tokio tasks.

use std::error::Error;
use std::fmt;
use std::num::NonZeroU16;
use std::sync::Arc;

use serde_json::Value;
use tokio::io::{AsyncRead, AsyncReadExt};
use tokio::process::{Child, ChildStderr, ChildStdin, ChildStdout};
use tokio::task::JoinHandle;

use crate::connection::{EngineConnectionError, EngineConnectionErrorKind};
use crate::diagnostic::{DiagnosticFailureKind, DiagnosticRouter, DiagnosticSnapshot, StreamKind};
use crate::discovery::LaunchExecutable;
use crate::launch::{
    CliSessionStart, ProcessSpawner, RetryClock, SessionSpawnError, SpawnSuccess, SpawnedProcess,
    spawn_with_retry,
};
use crate::preflight::CliLaunchRequest;
use crate::provisioning_control::ProvisioningCancellation;
use crate::session::SecretString;

const CONTROL_LINE_LIMIT: usize = 64 * 1024;
const STREAM_BUFFER_SIZE: usize = 16 * 1024;

/// Validated parameters emitted by the CLI session control record.
#[derive(Clone)]
pub(crate) struct SessionParameters {
    port: NonZeroU16,
    token: SecretString,
}

impl SessionParameters {
    pub(crate) const fn port(&self) -> NonZeroU16 {
        self.port
    }

    pub(crate) fn token(&self) -> &SecretString {
        &self.token
    }
}

impl fmt::Debug for SessionParameters {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("SessionParameters")
            .field("port", &self.port)
            .field("token", &self.token)
            .finish()
    }
}

/// Stable internal control-protocol classification.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ControlErrorKind {
    Eof,
    Oversize,
    Encoding,
    Json,
    FieldShape,
    PortRange,
    EmptyToken,
    EarlyExit,
}

/// Credential-safe control-protocol failure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct ControlError {
    kind: ControlErrorKind,
}

impl ControlError {
    const fn new(kind: ControlErrorKind) -> Self {
        Self { kind }
    }

    pub(crate) const fn kind(&self) -> ControlErrorKind {
        self.kind
    }
}

impl fmt::Display for ControlError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self.kind {
            ControlErrorKind::Eof => "CLI stdout ended before a complete control record",
            ControlErrorKind::Oversize => "the CLI control record exceeds 64 KiB",
            ControlErrorKind::Encoding => "the CLI control record is not UTF-8",
            ControlErrorKind::Json => "the CLI control record is not valid JSON",
            ControlErrorKind::FieldShape => "the CLI control record has invalid required fields",
            ControlErrorKind::PortRange => "the CLI control port is outside 1..=65535",
            ControlErrorKind::EmptyToken => "the CLI control token is empty",
            ControlErrorKind::EarlyExit => "the CLI exited before a valid control record",
        })
    }
}

impl Error for ControlError {}

pub(crate) struct ParsedControl {
    parameters: SessionParameters,
    suffix: Vec<u8>,
}

impl ParsedControl {
    pub(crate) fn parameters(&self) -> &SessionParameters {
        &self.parameters
    }

    pub(crate) fn suffix(&self) -> &[u8] {
        &self.suffix
    }
}

/// Incremental one-record parser used by both native I/O and arbitrary chunk tests.
pub(crate) struct ControlAccumulator {
    record: Vec<u8>,
    complete: bool,
}

impl ControlAccumulator {
    pub(crate) fn new() -> Self {
        Self {
            record: Vec::new(),
            complete: false,
        }
    }

    pub(crate) fn push(&mut self, chunk: &[u8]) -> Result<Option<ParsedControl>, ControlError> {
        if self.complete {
            return Ok(None);
        }
        if let Some(delimiter) = chunk.iter().position(|byte| *byte == b'\n') {
            let consumed = self.record.len().saturating_add(delimiter + 1);
            if consumed > CONTROL_LINE_LIMIT {
                return Err(ControlError::new(ControlErrorKind::Oversize));
            }
            self.record.extend_from_slice(&chunk[..delimiter]);
            let parameters = parse_control_record(&self.record)?;
            self.complete = true;
            return Ok(Some(ParsedControl {
                parameters,
                suffix: chunk[delimiter + 1..].to_vec(),
            }));
        }
        if self.record.len().saturating_add(chunk.len()) > CONTROL_LINE_LIMIT {
            return Err(ControlError::new(ControlErrorKind::Oversize));
        }
        self.record.extend_from_slice(chunk);
        Ok(None)
    }

    fn eof(&self) -> ControlError {
        ControlError::new(ControlErrorKind::Eof)
    }
}

fn parse_control_record(record: &[u8]) -> Result<SessionParameters, ControlError> {
    std::str::from_utf8(record).map_err(|_| ControlError::new(ControlErrorKind::Encoding))?;
    let value: Value =
        serde_json::from_slice(record).map_err(|_| ControlError::new(ControlErrorKind::Json))?;
    let object = value
        .as_object()
        .ok_or_else(|| ControlError::new(ControlErrorKind::FieldShape))?;
    let port = match object.get("port") {
        Some(Value::Number(port)) => port
            .as_u64()
            .ok_or_else(|| ControlError::new(ControlErrorKind::PortRange))?,
        Some(_) | None => return Err(ControlError::new(ControlErrorKind::FieldShape)),
    };
    let port = u16::try_from(port)
        .ok()
        .and_then(NonZeroU16::new)
        .ok_or_else(|| ControlError::new(ControlErrorKind::PortRange))?;
    let token = object
        .get("session_token")
        .and_then(Value::as_str)
        .ok_or_else(|| ControlError::new(ControlErrorKind::FieldShape))?;
    let token = SecretString::new(Arc::<str>::from(token))
        .ok_or_else(|| ControlError::new(ControlErrorKind::EmptyToken))?;
    Ok(SessionParameters { port, token })
}

async fn read_control_line<R>(reader: &mut R) -> Result<ParsedControl, ControlError>
where
    R: AsyncRead + Unpin,
{
    let mut parser = ControlAccumulator::new();
    let mut buffer = [0_u8; STREAM_BUFFER_SIZE];
    loop {
        let count = reader
            .read(&mut buffer)
            .await
            .map_err(|_| ControlError::new(ControlErrorKind::Eof))?;
        if count == 0 {
            return Err(parser.eof());
        }
        if let Some(parsed) = parser.push(&buffer[..count])? {
            return Ok(parsed);
        }
    }
}

/// Stable kind retained for a background component without source-controlled text.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum BackgroundFailureKind {
    Read,
    SinkRejected,
    SinkPanicked,
    Join,
    UnexpectedChildExit,
}

impl From<DiagnosticFailureKind> for BackgroundFailureKind {
    fn from(value: DiagnosticFailureKind) -> Self {
        match value {
            DiagnosticFailureKind::SinkRejected => Self::SinkRejected,
            DiagnosticFailureKind::SinkPanicked => Self::SinkPanicked,
        }
    }
}

/// One typed worker outcome retained through explicit session close.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct StreamOutcome {
    kind: StreamKind,
    bytes_seen: u64,
    failure: Option<BackgroundFailureKind>,
}

impl StreamOutcome {
    pub(crate) const fn kind(&self) -> StreamKind {
        self.kind
    }

    pub(crate) const fn bytes_seen(&self) -> u64 {
        self.bytes_seen
    }

    pub(crate) const fn failure(&self) -> Option<BackgroundFailureKind> {
        self.failure
    }

    #[cfg(test)]
    pub(crate) const fn for_test(
        kind: StreamKind,
        bytes_seen: u64,
        failure: Option<BackgroundFailureKind>,
    ) -> Self {
        Self {
            kind,
            bytes_seen,
            failure,
        }
    }
}

pub(crate) fn order_outcomes(outcomes: &mut [StreamOutcome]) {
    outcomes.sort_by_key(StreamOutcome::kind);
}

async fn drain_stream<R>(
    mut reader: R,
    initial: Vec<u8>,
    kind: StreamKind,
    router: Arc<DiagnosticRouter>,
) -> StreamOutcome
where
    R: AsyncRead + Unpin,
{
    let mut bytes_seen = initial.len() as u64;
    let mut failure = router.route(kind, &initial).sink_failure.map(Into::into);
    let mut buffer = [0_u8; STREAM_BUFFER_SIZE];
    loop {
        match reader.read(&mut buffer).await {
            Ok(0) => break,
            Ok(count) => {
                bytes_seen = bytes_seen.saturating_add(count as u64);
                if failure.is_none() {
                    failure = router
                        .route(kind, &buffer[..count])
                        .sink_failure
                        .map(Into::into);
                } else {
                    let _ = router.route(kind, &buffer[..count]);
                }
            }
            Err(_) => {
                failure.get_or_insert(BackgroundFailureKind::Read);
                break;
            }
        }
    }
    if failure.is_none() {
        failure = router.finish(kind).sink_failure.map(Into::into);
    } else {
        let _ = router.finish(kind);
    }
    if failure.is_none() {
        failure = router.channel_failure(kind).map(Into::into);
    }
    StreamOutcome {
        kind,
        bytes_seen,
        failure,
    }
}

/// Complete process-start failure after cleanup has converged.
pub(crate) enum SessionStartError {
    Spawn(SessionSpawnError),
    Protocol {
        error: ControlError,
        diagnostics: DiagnosticSnapshot,
    },
}

impl SessionStartError {
    pub(crate) fn protocol(&self) -> Option<&ControlError> {
        match self {
            Self::Spawn(_) => None,
            Self::Protocol { error, .. } => Some(error),
        }
    }

    pub(crate) fn diagnostics(&self) -> Option<&DiagnosticSnapshot> {
        match self {
            Self::Spawn(_) => None,
            Self::Protocol { diagnostics, .. } => Some(diagnostics),
        }
    }
}

impl fmt::Display for SessionStartError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Spawn(error) => error.fmt(formatter),
            Self::Protocol { error, .. } => error.fmt(formatter),
        }
    }
}

impl fmt::Debug for SessionStartError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Spawn(error) => formatter.debug_tuple("Spawn").field(error).finish(),
            Self::Protocol { error, diagnostics } => formatter
                .debug_struct("Protocol")
                .field("error", error)
                .field("diagnostics", diagnostics)
                .finish(),
        }
    }
}

impl Error for SessionStartError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Spawn(error) => Some(error),
            Self::Protocol { error, .. } => Some(error),
        }
    }
}

/// Statically dispatched launcher used by the concrete connector composition.
pub(crate) struct SessionLauncher<S, C> {
    spawner: S,
    clock: C,
}

impl<S, C> SessionLauncher<S, C> {
    pub(crate) const fn new(spawner: S, clock: C) -> Self {
        Self { spawner, clock }
    }
}

impl<S, C> SessionLauncher<S, C>
where
    S: ProcessSpawner<SpawnedProcess>,
    C: RetryClock,
{
    pub(crate) async fn launch(
        &mut self,
        start: CliSessionStart,
        cancellation: &ProvisioningCancellation,
    ) -> Result<StartedSession, SessionStartError> {
        let projection = start.projection();
        let initial_secrets = projection
            .environment()
            .iter()
            .map(|(_, value)| value.as_encoded_bytes().to_vec())
            .collect::<Vec<_>>();
        let (selected, options) = start.into_parts();
        let (executable, release_unavailable) = selected.into_parts();
        let spawned = spawn_with_retry(
            executable,
            &projection,
            &mut self.spawner,
            &self.clock,
            cancellation,
        )
        .await
        .map_err(|error| {
            SessionStartError::Spawn(SessionSpawnError::new(error, release_unavailable))
        })?;
        let mut pending = PendingResources::new(spawned, options, initial_secrets);
        match pending.establish().await {
            Ok(parameters) => Ok(StartedSession {
                parameters,
                resources: pending,
            }),
            Err(mut error) => {
                if error.kind() == ControlErrorKind::Eof && pending.child_exited().unwrap_or(false)
                {
                    error = ControlError::new(ControlErrorKind::EarlyExit);
                }
                pending.router.discard_sealed();
                let diagnostics = pending.router.snapshot();
                pending.cleanup().await;
                Err(SessionStartError::Protocol { error, diagnostics })
            }
        }
    }
}

/// Sole owner of every resource acquired before session transfer.
pub(crate) struct PendingResources {
    child: Option<Child>,
    stdin: Option<ChildStdin>,
    stdout: Option<ChildStdout>,
    stderr: Option<ChildStderr>,
    stdout_task: Option<JoinHandle<StreamOutcome>>,
    stderr_task: Option<JoinHandle<StreamOutcome>>,
    executable: Option<LaunchExecutable>,
    router: Arc<DiagnosticRouter>,
    armed: bool,
}

impl PendingResources {
    fn new(
        spawned: SpawnSuccess<SpawnedProcess>,
        options: CliLaunchRequest,
        initial_secrets: Vec<Vec<u8>>,
    ) -> Self {
        let SpawnSuccess {
            output, executable, ..
        } = spawned;
        let SpawnedProcess {
            child,
            stdin,
            stdout,
            stderr,
        } = output;
        let router = Arc::new(DiagnosticRouter::sealed(
            Arc::clone(&options.diagnostics),
            initial_secrets,
        ));
        let mut pending = Self {
            child: Some(child),
            stdin: Some(stdin),
            stdout: Some(stdout),
            stderr: Some(stderr),
            stdout_task: None,
            stderr_task: None,
            executable: Some(executable),
            router,
            armed: true,
        };
        // Construct the complete owner before allowing the stderr task to run. A task
        // handle is installed synchronously, so cancellation cannot detach it.
        pending.start_stderr();
        pending
    }

    fn start_stderr(&mut self) {
        let Some(stderr) = self.stderr.take() else {
            return;
        };
        let router = Arc::clone(&self.router);
        self.stderr_task = Some(tokio::spawn(drain_stream(
            stderr,
            Vec::new(),
            StreamKind::Stderr,
            router,
        )));
    }

    async fn establish(&mut self) -> Result<SessionParameters, ControlError> {
        let mut stdout = self
            .stdout
            .take()
            .ok_or_else(|| ControlError::new(ControlErrorKind::Eof))?;
        let parsed = read_control_line(&mut stdout).await?;
        let _ = self
            .router
            .activate(parsed.parameters.token.expose_for_redaction());
        let router = Arc::clone(&self.router);
        self.stdout_task = Some(tokio::spawn(drain_stream(
            stdout,
            parsed.suffix,
            StreamKind::Stdout,
            router,
        )));
        Ok(parsed.parameters)
    }

    fn child_exited(&mut self) -> Result<bool, std::io::Error> {
        match self.child.as_mut() {
            Some(child) => child.try_wait().map(|status| status.is_some()),
            None => Ok(true),
        }
    }

    async fn cleanup(mut self) {
        self.cleanup_owned().await;
        self.armed = false;
    }

    pub(crate) fn transfer(mut self) -> SessionResources {
        self.armed = false;
        SessionResources {
            child: self.child.take(),
            stdin: self.stdin.take(),
            stdout_task: self.stdout_task.take(),
            stderr_task: self.stderr_task.take(),
            executable: self.executable.take(),
            router: Arc::clone(&self.router),
        }
    }

    async fn cleanup_owned(&mut self) {
        self.stdin.take();
        self.stdout.take();
        self.stderr.take();
        if let Some(mut child) = self.child.take() {
            let _ = child.start_kill();
            let _ = child.wait().await;
        }
        for task in [&mut self.stdout_task, &mut self.stderr_task] {
            if let Some(handle) = task.take() {
                handle.abort();
                let _ = handle.await;
            }
        }
        self.executable.take();
    }
}

impl Drop for PendingResources {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        reap_on_drop(
            self.child.take(),
            self.stdin.take(),
            self.stdout_task.take(),
            self.stderr_task.take(),
            self.executable.take(),
        );
    }
}

/// Parsed session and still-armed pre-connection resources.
pub(crate) struct StartedSession {
    parameters: SessionParameters,
    resources: PendingResources,
}

impl fmt::Debug for StartedSession {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("StartedSession")
            .field("parameters", &self.parameters)
            .field("resources", &"owned")
            .finish()
    }
}

impl StartedSession {
    pub(crate) fn parameters(&self) -> &SessionParameters {
        &self.parameters
    }

    pub(crate) fn transfer(self) -> (SessionParameters, SessionResources) {
        (self.parameters, self.resources.transfer())
    }
}

/// Process resources after the one successful pending-to-session transfer.
pub(crate) struct SessionResources {
    child: Option<Child>,
    stdin: Option<ChildStdin>,
    stdout_task: Option<JoinHandle<StreamOutcome>>,
    stderr_task: Option<JoinHandle<StreamOutcome>>,
    executable: Option<LaunchExecutable>,
    router: Arc<DiagnosticRouter>,
}

/// Deterministic terminal observation of child and background worker state.
pub(crate) struct SessionCloseReport {
    stream_outcomes: Vec<StreamOutcome>,
    child_failure: Option<BackgroundFailureKind>,
    diagnostics: DiagnosticSnapshot,
}

impl SessionCloseReport {
    pub(crate) fn stream_outcomes(&self) -> &[StreamOutcome] {
        &self.stream_outcomes
    }

    pub(crate) const fn child_failure(&self) -> Option<BackgroundFailureKind> {
        self.child_failure
    }

    pub(crate) fn diagnostics(&self) -> &DiagnosticSnapshot {
        &self.diagnostics
    }

    #[cfg(test)]
    pub(crate) fn for_test(
        mut stream_outcomes: Vec<StreamOutcome>,
        child_failure: Option<BackgroundFailureKind>,
    ) -> Self {
        order_outcomes(&mut stream_outcomes);
        Self {
            stream_outcomes,
            child_failure,
            diagnostics: DiagnosticSnapshot::default(),
        }
    }
}

impl SessionResources {
    pub(crate) async fn close(mut self) -> Result<SessionCloseReport, EngineConnectionError> {
        self.stdin.take();
        let child_failure = if let Some(mut child) = self.child.take() {
            let status = child.wait().await.map_err(|error| {
                EngineConnectionError::with_source(EngineConnectionErrorKind::Transport, error)
            })?;
            (!status.success()).then_some(BackgroundFailureKind::UnexpectedChildExit)
        } else {
            None
        };

        let mut outcomes = Vec::with_capacity(3);
        for (kind, task) in [
            (StreamKind::Stdout, self.stdout_task.take()),
            (StreamKind::Stderr, self.stderr_task.take()),
        ] {
            if let Some(task) = task {
                match task.await {
                    Ok(outcome) => outcomes.push(outcome),
                    Err(_) => outcomes.push(StreamOutcome {
                        kind,
                        bytes_seen: 0,
                        failure: Some(BackgroundFailureKind::Join),
                    }),
                }
            }
        }
        order_outcomes(&mut outcomes);
        self.executable.take();
        Ok(SessionCloseReport {
            stream_outcomes: outcomes,
            child_failure,
            diagnostics: self.router.snapshot(),
        })
    }

    pub(crate) fn diagnostics(&self) -> DiagnosticSnapshot {
        self.router.snapshot()
    }
}

impl Drop for SessionResources {
    fn drop(&mut self) {
        reap_on_drop(
            self.child.take(),
            self.stdin.take(),
            self.stdout_task.take(),
            self.stderr_task.take(),
            self.executable.take(),
        );
    }
}

fn reap_on_drop(
    mut child: Option<Child>,
    stdin: Option<ChildStdin>,
    stdout_task: Option<JoinHandle<StreamOutcome>>,
    stderr_task: Option<JoinHandle<StreamOutcome>>,
    executable: Option<LaunchExecutable>,
) {
    drop(stdin);
    if let Some(child) = &mut child {
        let _ = child.start_kill();
    }
    if let Some(task) = &stdout_task {
        task.abort();
    }
    if let Some(task) = &stderr_task {
        task.abort();
    }
    if let Ok(runtime) = tokio::runtime::Handle::try_current() {
        runtime.spawn(async move {
            if let Some(mut child) = child {
                let _ = child.wait().await;
            }
            if let Some(task) = stdout_task {
                let _ = task.await;
            }
            if let Some(task) = stderr_task {
                let _ = task.await;
            }
            drop(executable);
        });
    }
    // Outside a runtime, kill-on-drop and aborted worker handles are the synchronous
    // backstop. The lease is released only after termination has at least begun.
}
