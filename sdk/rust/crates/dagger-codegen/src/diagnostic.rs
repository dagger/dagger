//! Structured diagnostics produced by pure code-generation stages.

use thiserror::Error;

/// A stable category for a code-generation failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DiagnosticKind {
    /// Raw introspection input could not be decoded.
    Decode,
    /// The target contract is invalid or unsupported.
    Target,
    /// The schema violates a canonicalization invariant.
    Schema,
    /// Rust syntax could not be constructed or validated.
    Render,
}

/// A caller-actionable code-generation failure.
#[derive(Debug, Error)]
#[error("{kind:?}: {message}")]
pub struct CodegenError {
    kind: DiagnosticKind,
    message: String,
}

impl CodegenError {
    /// Creates a diagnostic with a stable category and human-readable explanation.
    #[must_use]
    pub fn new(kind: DiagnosticKind, message: impl Into<String>) -> Self {
        Self {
            kind,
            message: message.into(),
        }
    }

    /// Returns the stable diagnostic category.
    #[must_use]
    pub const fn kind(&self) -> DiagnosticKind {
        self.kind
    }
}
