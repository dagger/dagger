//! Stable abstraction for caller-supplied Dagger engine connections.
//!
//! Transferring a boxed [`EngineConnection`] gives the eventual owned client sole
//! responsibility for graceful close and the non-blocking abort backstop. Implementors
//! must keep cancellation of one request local to that request and must never include
//! credentials in ordinary error rendering.

use std::error::Error;
use std::fmt;
use std::sync::Arc;

use async_trait::async_trait;

use crate::graphql::{RawRequest, RawResponse};

/// Stable category for a failure returned by an engine connection.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EngineConnectionErrorKind {
    /// The underlying transport failed.
    Transport,
    /// A protocol invariant was violated.
    Protocol,
    /// The selected engine or endpoint is unavailable.
    Unavailable,
    /// The connection has already been closed.
    Closed,
    /// SDK-owned HTTP connection establishment exceeded its configured bound.
    ConnectTimeout,
    /// A source-specific failure without a more precise stable category.
    Other,
}

impl EngineConnectionErrorKind {
    const fn description(self) -> &'static str {
        match self {
            Self::Transport => "the engine transport failed",
            Self::Protocol => "the engine protocol failed",
            Self::Unavailable => "the engine connection is unavailable",
            Self::Closed => "the engine connection is closed",
            Self::ConnectTimeout => "the engine HTTP connection timed out",
            Self::Other => "the engine connection failed",
        }
    }
}

/// A typed connection failure which retains, but does not ordinarily render, its cause.
#[derive(Clone)]
pub struct EngineConnectionError {
    kind: EngineConnectionErrorKind,
    source: Option<Arc<dyn Error + Send + Sync + 'static>>,
}

impl EngineConnectionError {
    /// Creates a synthetic typed failure without an opaque cause.
    pub fn new(kind: EngineConnectionErrorKind) -> Self {
        Self { kind, source: None }
    }

    /// Creates a typed failure while retaining the implementation's original cause.
    pub fn with_source<E>(kind: EngineConnectionErrorKind, source: E) -> Self
    where
        E: Error + Send + Sync + 'static,
    {
        Self {
            kind,
            source: Some(Arc::new(source)),
        }
    }

    /// Returns the stable connection failure category.
    pub const fn kind(&self) -> EngineConnectionErrorKind {
        self.kind
    }
}

impl fmt::Display for EngineConnectionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.kind.description())
    }
}

impl fmt::Debug for EngineConnectionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("EngineConnectionError")
            .field("kind", &self.kind)
            .finish_non_exhaustive()
    }
}

impl Error for EngineConnectionError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.source
            .as_deref()
            .map(|source| source as &(dyn Error + 'static))
    }
}

/// A transferred, thread-safe connection used by raw and generated requests.
///
/// The client invokes [`EngineConnection::close`] exactly once through its shared
/// close election. [`EngineConnection::abort`] is a prompt, non-blocking backstop used
/// when async cleanup cannot be completed; implementations must make it idempotent and
/// non-panicking. Dropping or cancelling one `execute` future must not invalidate
/// unrelated requests.
#[async_trait]
pub trait EngineConnection: Send + Sync + 'static {
    /// Executes one complete raw GraphQL request.
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError>;

    /// Gracefully releases every resource owned by this connection.
    async fn close(&self) -> Result<(), EngineConnectionError>;

    /// Starts the non-blocking emergency cleanup path.
    fn abort(&self);
}
