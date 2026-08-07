//! Caller-controlled progress and lifecycle diagnostic contracts.
//!
//! Diagnostics contain already-sanitized bytes. The private dispatcher introduced by
//! the connection pipeline owns ordering and failure containment; sinks must return
//! promptly and never assume that final-handle drop will flush pending events.

use std::error::Error;
use std::fmt;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::Arc;
use std::sync::Mutex;

const DIAGNOSTIC_TAIL_LIMIT: usize = 1024 * 1024;
const REDACTION: &[u8] = b"[REDACTED]";

/// Origin of a diagnostic payload.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DiagnosticStream {
    /// Ordinary CLI standard output after the session control line is consumed.
    Stdout,
    /// Ordinary CLI standard error.
    Stderr,
    /// A redacted SDK lifecycle event.
    Lifecycle,
}

/// One ordered, borrowed diagnostic payload.
#[derive(Clone, Copy)]
pub struct Diagnostic<'a> {
    /// The source stream.
    pub stream: DiagnosticStream,
    /// Sanitized bytes in source order.
    pub payload: &'a [u8],
}

impl fmt::Debug for Diagnostic<'_> {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("Diagnostic")
            .field("stream", &self.stream)
            .field("payload_len", &self.payload.len())
            .finish()
    }
}

/// A sink callback failure which is retained for explicit inspection only.
#[derive(Clone)]
pub struct DiagnosticSinkError {
    source: Option<Arc<dyn Error + Send + Sync + 'static>>,
}

impl DiagnosticSinkError {
    /// Creates a synthetic sink failure without an underlying cause.
    pub fn new() -> Self {
        Self { source: None }
    }

    /// Creates a sink failure which retains the caller's original cause.
    pub fn with_source<E>(source: E) -> Self
    where
        E: Error + Send + Sync + 'static,
    {
        Self {
            source: Some(Arc::new(source)),
        }
    }
}

impl Default for DiagnosticSinkError {
    fn default() -> Self {
        Self::new()
    }
}

impl fmt::Display for DiagnosticSinkError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("the diagnostic sink rejected an event")
    }
}

impl fmt::Debug for DiagnosticSinkError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("DiagnosticSinkError { .. }")
    }
}

impl Error for DiagnosticSinkError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.source
            .as_deref()
            .map(|source| source as &(dyn Error + 'static))
    }
}

/// Prompt, thread-safe destination for optional CLI and lifecycle diagnostics.
///
/// Implementations must not block indefinitely. Returning an error or unwinding does
/// not fail connection or close: the SDK disables the sink and continues with a
/// redacted internal trace. Session parameters and credentials are never delivered.
pub trait DiagnosticSink: Send + Sync + 'static {
    /// Accepts one sanitized event in ingestion order.
    fn emit(&self, diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError>;
}

/// Internal input accepted by the single ordered diagnostic dispatcher.
///
/// The CLI session-parameter line is represented without its bytes. That control
/// payload can contain the session token, so making it impossible to hand those bytes
/// to the sink is stronger than relying on every caller to remember redaction.
#[derive(Clone, Copy)]
#[allow(dead_code)] // The CLI stream reader is not yet wired to this dispatcher.
pub(crate) enum DiagnosticInput<'a> {
    SessionControl,
    Progress(Diagnostic<'a>),
}

/// Safe outcome of one contained sink invocation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum DiagnosticDispatchOutcome {
    Delivered,
    Disabled,
    Failed(DiagnosticFailureKind),
}

/// Stable background classification which never retains callback-controlled text.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum DiagnosticFailureKind {
    SinkRejected,
    SinkPanicked,
}

/// Serializes optional sink callbacks and permanently contains the first sink failure.
#[allow(dead_code)] // Connection planning is not yet wired into the public client path.
pub(crate) struct DiagnosticDispatcher {
    sink: Mutex<Option<Arc<dyn DiagnosticSink>>>,
}

#[allow(dead_code)]
impl DiagnosticDispatcher {
    pub(crate) fn new(sink: Option<Arc<dyn DiagnosticSink>>) -> Self {
        Self {
            sink: Mutex::new(sink),
        }
    }

    /// Ingests one already-sanitized event without affecting its owning operation.
    pub(crate) fn ingest(&self, input: DiagnosticInput<'_>) -> DiagnosticDispatchOutcome {
        let DiagnosticInput::Progress(diagnostic) = input else {
            return DiagnosticDispatchOutcome::Disabled;
        };

        // The guard is intentionally retained across the callback: this is the one
        // serialization point which gives mixed stdout/stderr ingestion one observable
        // order. The dispatcher is private, so a caller sink cannot re-enter it.
        let mut sink = match self.sink.lock() {
            Ok(sink) => sink,
            Err(poisoned) => poisoned.into_inner(),
        };
        let Some(active) = sink.as_ref().cloned() else {
            return DiagnosticDispatchOutcome::Disabled;
        };

        let outcome = catch_unwind(AssertUnwindSafe(|| active.emit(diagnostic)));
        match outcome {
            Ok(Ok(())) => DiagnosticDispatchOutcome::Delivered,
            Ok(Err(_)) => {
                *sink = None;
                // The callback error is caller-controlled and may contain credentials,
                // so only the stable category crosses the containment boundary.
                tracing::warn!(
                    target: "dagger_sdk::diagnostics",
                    "diagnostic sink disabled after callback failure"
                );
                DiagnosticDispatchOutcome::Failed(DiagnosticFailureKind::SinkRejected)
            }
            Err(_) => {
                *sink = None;
                // Panic payloads receive the same treatment: formatting one would
                // reintroduce arbitrary caller bytes into otherwise safe telemetry.
                tracing::warn!(
                    target: "dagger_sdk::diagnostics",
                    "diagnostic sink disabled after callback panic"
                );
                DiagnosticDispatchOutcome::Failed(DiagnosticFailureKind::SinkPanicked)
            }
        }
    }
}

/// Internal stream identity used before the public diagnostic projection.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub(crate) enum StreamKind {
    Stdout,
    Stderr,
}

impl StreamKind {
    const fn diagnostic(self) -> DiagnosticStream {
        match self {
            Self::Stdout => DiagnosticStream::Stdout,
            Self::Stderr => DiagnosticStream::Stderr,
        }
    }
}

/// Streaming byte redactor which retains only an unresolved secret-prefix suffix.
#[derive(Clone)]
pub(crate) struct SecretRedactor {
    patterns: Vec<Vec<u8>>,
    pending: Vec<u8>,
}

impl SecretRedactor {
    pub(crate) fn new(secrets: impl IntoIterator<Item = Vec<u8>>) -> Self {
        let mut patterns = secrets
            .into_iter()
            .filter(|secret| !secret.is_empty())
            .collect::<Vec<_>>();
        patterns.sort_by(|left, right| right.len().cmp(&left.len()).then(left.cmp(right)));
        patterns.dedup();
        Self {
            patterns,
            pending: Vec::new(),
        }
    }

    pub(crate) fn push(&mut self, chunk: &[u8]) -> Vec<u8> {
        let mut output = Vec::with_capacity(chunk.len());
        for byte in chunk {
            self.pending.push(*byte);
            self.drain(false, &mut output);
        }
        output
    }

    pub(crate) fn finish(&mut self) -> Vec<u8> {
        let mut output = Vec::with_capacity(self.pending.len());
        self.drain(true, &mut output);
        output
    }

    fn drain(&mut self, finalizing: bool, output: &mut Vec<u8>) {
        while !self.pending.is_empty() {
            let completed = self
                .patterns
                .iter()
                .find(|pattern| self.pending.starts_with(pattern.as_slice()))
                .map(Vec::len);
            let can_still_grow = self.patterns.iter().any(|pattern| {
                pattern.len() > self.pending.len() && pattern.starts_with(&self.pending)
            });

            if let Some(length) = completed
                && (finalizing || self.pending.len() > length || !can_still_grow)
            {
                output.extend_from_slice(REDACTION);
                self.pending.drain(..length);
                continue;
            }
            if !finalizing
                && self
                    .patterns
                    .iter()
                    .any(|pattern| pattern.starts_with(&self.pending))
            {
                break;
            }
            output.push(self.pending.remove(0));
        }
    }
}

/// Fixed-capacity suffix retained only after redaction.
#[derive(Clone, Default)]
struct BoundedTail {
    bytes: Vec<u8>,
}

impl BoundedTail {
    fn push(&mut self, bytes: &[u8]) {
        if bytes.len() >= DIAGNOSTIC_TAIL_LIMIT {
            self.bytes.clear();
            self.bytes
                .extend_from_slice(&bytes[bytes.len() - DIAGNOSTIC_TAIL_LIMIT..]);
            return;
        }
        let overflow = self
            .bytes
            .len()
            .saturating_add(bytes.len())
            .saturating_sub(DIAGNOSTIC_TAIL_LIMIT);
        if overflow > 0 {
            self.bytes.drain(..overflow);
        }
        self.bytes.extend_from_slice(bytes);
    }

    fn snapshot(&self) -> Vec<u8> {
        self.bytes.clone()
    }
}

struct ActiveChannel {
    redactor: SecretRedactor,
    tail: BoundedTail,
    sink_failure: Option<DiagnosticFailureKind>,
}

impl ActiveChannel {
    fn new(secrets: &[Vec<u8>]) -> Self {
        Self {
            redactor: SecretRedactor::new(secrets.iter().cloned()),
            tail: BoundedTail::default(),
            sink_failure: None,
        }
    }
}

enum DiagnosticGate {
    Sealed {
        stderr: BoundedTail,
        initial_secrets: Vec<Vec<u8>>,
    },
    Active {
        stdout: ActiveChannel,
        stderr: ActiveChannel,
    },
    Discarded,
}

/// Redacted bounded diagnostic material retained independently per child stream.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct DiagnosticSnapshot {
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

impl DiagnosticSnapshot {
    #[cfg_attr(not(test), allow(dead_code))]
    pub(crate) fn stdout(&self) -> &[u8] {
        &self.stdout
    }

    #[cfg_attr(not(test), allow(dead_code))]
    pub(crate) fn stderr(&self) -> &[u8] {
        &self.stderr
    }
}

/// Result of routing one stream chunk through redaction and sink containment.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct DiagnosticRouteOutcome {
    pub(crate) sink_failure: Option<DiagnosticFailureKind>,
}

/// Shared sealed-to-active router for CLI process diagnostics.
pub(crate) struct DiagnosticRouter {
    dispatcher: Arc<DiagnosticDispatcher>,
    gate: Mutex<DiagnosticGate>,
}

impl DiagnosticRouter {
    pub(crate) fn sealed(
        dispatcher: Arc<DiagnosticDispatcher>,
        initial_secrets: impl IntoIterator<Item = Vec<u8>>,
    ) -> Self {
        Self {
            dispatcher,
            gate: Mutex::new(DiagnosticGate::Sealed {
                stderr: BoundedTail::default(),
                initial_secrets: initial_secrets
                    .into_iter()
                    .filter(|secret| !secret.is_empty())
                    .collect(),
            }),
        }
    }

    /// Installs the trustworthy control token before any sealed stderr is released.
    pub(crate) fn activate(&self, token: &[u8]) -> DiagnosticRouteOutcome {
        let mut gate = self.lock_gate();
        if !matches!(&*gate, DiagnosticGate::Sealed { .. }) {
            return DiagnosticRouteOutcome { sink_failure: None };
        }
        let DiagnosticGate::Sealed {
            stderr,
            mut initial_secrets,
        } = std::mem::replace(&mut *gate, DiagnosticGate::Discarded)
        else {
            return DiagnosticRouteOutcome { sink_failure: None };
        };
        if !token.is_empty() {
            initial_secrets.push(token.to_vec());
        }
        let sealed = stderr.snapshot();
        *gate = DiagnosticGate::Active {
            stdout: ActiveChannel::new(&initial_secrets),
            stderr: ActiveChannel::new(&initial_secrets),
        };
        self.route_locked(&mut gate, StreamKind::Stderr, &sealed, false)
    }

    /// Discards pre-token bytes when startup never establishes a trustworthy token.
    pub(crate) fn discard_sealed(&self) {
        let mut gate = self.lock_gate();
        if matches!(&*gate, DiagnosticGate::Sealed { .. }) {
            *gate = DiagnosticGate::Discarded;
        }
    }

    pub(crate) fn route(&self, kind: StreamKind, chunk: &[u8]) -> DiagnosticRouteOutcome {
        let mut gate = self.lock_gate();
        self.route_locked(&mut gate, kind, chunk, false)
    }

    pub(crate) fn finish(&self, kind: StreamKind) -> DiagnosticRouteOutcome {
        let mut gate = self.lock_gate();
        self.route_locked(&mut gate, kind, &[], true)
    }

    pub(crate) fn snapshot(&self) -> DiagnosticSnapshot {
        match &*self.lock_gate() {
            DiagnosticGate::Active { stdout, stderr } => DiagnosticSnapshot {
                stdout: stdout.tail.snapshot(),
                stderr: stderr.tail.snapshot(),
            },
            // Raw sealed bytes may contain the not-yet-known token. Returning an empty
            // snapshot is the only safe failure representation before activation.
            DiagnosticGate::Sealed { .. } | DiagnosticGate::Discarded => {
                DiagnosticSnapshot::default()
            }
        }
    }

    pub(crate) fn channel_failure(&self, kind: StreamKind) -> Option<DiagnosticFailureKind> {
        match &*self.lock_gate() {
            DiagnosticGate::Active { stdout, stderr } => match kind {
                StreamKind::Stdout => stdout.sink_failure,
                StreamKind::Stderr => stderr.sink_failure,
            },
            DiagnosticGate::Sealed { .. } | DiagnosticGate::Discarded => None,
        }
    }

    fn route_locked(
        &self,
        gate: &mut DiagnosticGate,
        kind: StreamKind,
        chunk: &[u8],
        finish: bool,
    ) -> DiagnosticRouteOutcome {
        let DiagnosticGate::Active { stdout, stderr } = gate else {
            if let DiagnosticGate::Sealed { stderr, .. } = gate
                && kind == StreamKind::Stderr
            {
                stderr.push(chunk);
            }
            return DiagnosticRouteOutcome { sink_failure: None };
        };
        let channel = match kind {
            StreamKind::Stdout => stdout,
            StreamKind::Stderr => stderr,
        };
        let sanitized = if finish {
            channel.redactor.finish()
        } else {
            channel.redactor.push(chunk)
        };
        channel.tail.push(&sanitized);
        let dispatch = if sanitized.is_empty() {
            DiagnosticDispatchOutcome::Disabled
        } else {
            self.dispatcher
                .ingest(DiagnosticInput::Progress(Diagnostic {
                    stream: kind.diagnostic(),
                    payload: &sanitized,
                }))
        };
        let sink_failure = match dispatch {
            DiagnosticDispatchOutcome::Failed(failure) => Some(failure),
            DiagnosticDispatchOutcome::Delivered | DiagnosticDispatchOutcome::Disabled => None,
        };
        if channel.sink_failure.is_none() {
            channel.sink_failure = sink_failure;
        }
        DiagnosticRouteOutcome { sink_failure }
    }

    fn lock_gate(&self) -> std::sync::MutexGuard<'_, DiagnosticGate> {
        self.gate
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}
