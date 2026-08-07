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
    pub(crate) fn ingest(&self, input: DiagnosticInput<'_>) {
        let DiagnosticInput::Progress(diagnostic) = input else {
            return;
        };

        // The guard is intentionally retained across the callback: this is the one
        // serialization point which gives mixed stdout/stderr ingestion one observable
        // order. The dispatcher is private, so a caller sink cannot re-enter it.
        let mut sink = match self.sink.lock() {
            Ok(sink) => sink,
            Err(poisoned) => poisoned.into_inner(),
        };
        let Some(active) = sink.as_ref().cloned() else {
            return;
        };

        let outcome = catch_unwind(AssertUnwindSafe(|| active.emit(diagnostic)));
        if !matches!(outcome, Ok(Ok(()))) {
            *sink = None;
            // Neither the sink error nor a panic payload is formatted here: both are
            // caller-controlled and may contain credentials or environment values.
            tracing::warn!(
                target: "dagger_sdk::diagnostics",
                "diagnostic sink disabled after callback failure"
            );
        }
    }
}
