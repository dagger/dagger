//! Caller-controlled progress and lifecycle diagnostic contracts.
//!
//! Diagnostics contain already-sanitized bytes. The private dispatcher introduced by
//! the connection pipeline owns ordering and failure containment; sinks must return
//! promptly and never assume that final-handle drop will flush pending events.

use std::error::Error;
use std::fmt;
use std::sync::Arc;

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

    pub(crate) fn with_boxed_source(source: Box<dyn Error + Send + Sync + 'static>) -> Self {
        Self {
            source: Some(Arc::from(source)),
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
