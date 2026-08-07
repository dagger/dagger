//! Shared ownership and shutdown for one Dagger engine session.
//!
//! Application handles carry explicit leases while shutdown tasks carry only internal
//! `Arc` ownership. Keeping those two counts separate makes the final application
//! handle observable without mistaking an in-flight close task for a live caller. The
//! lifecycle has one linearization point (`Open -> Closing`), one terminal result, and
//! one non-blocking abort backstop.

use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::{AtomicBool, AtomicU8, AtomicUsize, Ordering};
use std::sync::{Arc, OnceLock};
use std::time::Duration;

use futures::FutureExt;
use tokio::sync::Notify;

use crate::connection::EngineConnection;
use crate::connector::map_connection_error;
use crate::errors::{CloseError, RequestError};
use crate::graphql::{RawRequest, RawResponse};

#[repr(u8)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum LifecycleState {
    Open = 0,
    Closing = 1,
    Closed = 2,
}

impl LifecycleState {
    fn load(value: u8) -> Self {
        match value {
            value if value == Self::Open as u8 => Self::Open,
            value if value == Self::Closing as u8 => Self::Closing,
            _ => Self::Closed,
        }
    }
}

type TerminalCloseResult = Result<(), CloseError>;

/// The one connection and lifecycle state shared by every external session lease.
pub(crate) struct SharedSession {
    state: AtomicU8,
    leases: AtomicUsize,
    abort_started: AtomicBool,
    terminal: OnceLock<TerminalCloseResult>,
    closed: Notify,
    connection: Arc<dyn EngineConnection>,
    http_connect_timeout: Option<Duration>,
    execution_timeout: Option<Duration>,
}

impl SharedSession {
    fn new(
        connection: Box<dyn EngineConnection>,
        http_connect_timeout: Option<Duration>,
        execution_timeout: Option<Duration>,
    ) -> Arc<Self> {
        Arc::new(Self {
            state: AtomicU8::new(LifecycleState::Open as u8),
            leases: AtomicUsize::new(1),
            abort_started: AtomicBool::new(false),
            terminal: OnceLock::new(),
            closed: Notify::new(),
            connection: Arc::from(connection),
            http_connect_timeout,
            execution_timeout,
        })
    }

    fn state(&self) -> LifecycleState {
        // Acquire pairs with terminal publication's Release state transition: once a
        // caller observes Closed, the preceding OnceLock write is also visible.
        LifecycleState::load(self.state.load(Ordering::Acquire))
    }

    fn elect_close(&self) -> bool {
        self.state
            .compare_exchange(
                LifecycleState::Open as u8,
                LifecycleState::Closing as u8,
                Ordering::AcqRel,
                Ordering::Acquire,
            )
            .is_ok()
    }

    async fn close(self: &Arc<Self>) -> TerminalCloseResult {
        if self.elect_close() {
            self.spawn_close_task();
        }
        self.wait_for_terminal().await
    }

    fn spawn_close_task(self: &Arc<Self>) {
        let Ok(runtime) = tokio::runtime::Handle::try_current() else {
            // Explicit close can be driven by a non-Tokio executor. The connection
            // contract offers no portable synchronous close, so abort is the only
            // non-blocking path which cannot strand the resource.
            self.abort_once();
            self.publish_terminal(Err(CloseError::Interrupted));
            return;
        };

        let session = Arc::clone(self);
        // Construct the guard before handing the future to Tokio. A runtime can reject
        // or discard a task before its first poll; keeping the guard in the future's
        // captured state makes that path publish Interrupted instead of leaving the
        // lifecycle permanently Closing.
        let mut completion = CloseCompletionGuard::new(Arc::clone(&session));
        let close_future = async move {
            let outcome = AssertUnwindSafe(session.connection.close())
                .catch_unwind()
                .await;
            let result = match outcome {
                Ok(Ok(())) => Ok(()),
                Ok(Err(error)) => Err(CloseError::Connection(error)),
                Err(_) => Err(CloseError::Panicked),
            };
            completion.complete(result);
        };
        if catch_unwind(AssertUnwindSafe(|| runtime.spawn(close_future))).is_err() {
            // The captured completion guard has already published Interrupted while
            // unwinding the rejected task; only the static operational fact is safe to
            // report because a runtime panic payload is caller-controlled process data.
            tracing::warn!("runtime rejected engine connection close task");
        }
    }

    async fn wait_for_terminal(&self) -> TerminalCloseResult {
        loop {
            if self.state() == LifecycleState::Closed
                && let Some(result) = self.terminal.get()
            {
                return result.clone();
            }

            // Notify does not retain permits for notify_waiters. Registering before
            // the second terminal check closes the otherwise possible lost-wake gap.
            let notified = self.closed.notified();
            if self.state() == LifecycleState::Closed
                && let Some(result) = self.terminal.get()
            {
                return result.clone();
            }
            notified.await;
        }
    }

    fn publish_terminal(&self, result: TerminalCloseResult) {
        let _ = self.terminal.set(result);
        // Terminal storage precedes the Release transition so Closed is never visible
        // without the immutable close outcome that every waiter must reuse.
        self.state
            .store(LifecycleState::Closed as u8, Ordering::Release);
        self.closed.notify_waiters();
    }

    fn abort_once(&self) {
        if self
            .abort_started
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .is_err()
        {
            return;
        }

        if catch_unwind(AssertUnwindSafe(|| self.connection.abort())).is_err() {
            // Never include an implementation-authored panic payload: injected
            // connections may carry credentials or endpoint material in it.
            tracing::warn!("engine connection abort panicked");
        }
    }

    async fn execute(&self, request: RawRequest) -> Result<RawResponse, RequestError> {
        // This Acquire load is the request admission point. A close linearized first
        // prevents all transport work; a request admitted first may race resource
        // shutdown and is classified against the later lifecycle state.
        if self.state() != LifecycleState::Open {
            return Err(RequestError::ClientClosed);
        }

        let execution = AssertUnwindSafe(self.connection.execute(request)).catch_unwind();
        let outcome = match self.execution_timeout {
            Some(duration) => match tokio::time::timeout(duration, execution).await {
                Ok(outcome) => outcome,
                Err(_) if self.state() != LifecycleState::Open => {
                    return Err(RequestError::InterruptedByClose);
                }
                Err(_) => return Err(RequestError::ExecutionTimeout { duration }),
            },
            None => execution.await,
        };

        match outcome {
            Ok(Ok(response)) => Ok(response),
            Ok(Err(_)) if self.state() != LifecycleState::Open => {
                Err(RequestError::InterruptedByClose)
            }
            Ok(Err(error)) => Err(map_connection_error(error, self.http_connect_timeout)),
            Err(_) if self.state() != LifecycleState::Open => Err(RequestError::InterruptedByClose),
            Err(_) => Err(RequestError::ConnectionPanicked),
        }
    }

    fn final_lease_dropped(self: &Arc<Self>) {
        if !self.elect_close() {
            return;
        }

        if tokio::runtime::Handle::try_current().is_ok() {
            self.spawn_close_task();
        } else {
            // Drop must never wait or require a runtime. Abort is specified as the
            // implementation's prompt idempotent cleanup path.
            self.abort_once();
            self.publish_terminal(Err(CloseError::Interrupted));
        }
    }

    #[cfg(test)]
    fn lifecycle_state(&self) -> LifecycleState {
        self.state()
    }
}

impl Drop for SharedSession {
    fn drop(&mut self) {
        // This is the ultimate backstop for executor teardown or an abandoned internal
        // shutdown owner. Closed resources deliberately avoid a redundant abort.
        if self.state() != LifecycleState::Closed {
            self.abort_once();
        }
    }
}

/// One externally observable lease on a shared engine session.
pub(crate) struct SessionHandle {
    shared: Arc<SharedSession>,
}

impl SessionHandle {
    pub(crate) fn new(
        connection: Box<dyn EngineConnection>,
        http_connect_timeout: Option<Duration>,
        execution_timeout: Option<Duration>,
    ) -> Self {
        Self {
            shared: SharedSession::new(connection, http_connect_timeout, execution_timeout),
        }
    }

    pub(crate) async fn execute(&self, request: RawRequest) -> Result<RawResponse, RequestError> {
        self.shared.execute(request).await
    }

    pub(crate) async fn close(&self) -> TerminalCloseResult {
        self.shared.close().await
    }

    #[cfg(test)]
    pub(crate) fn lifecycle_state(&self) -> &'static str {
        match self.shared.lifecycle_state() {
            LifecycleState::Open => "open",
            LifecycleState::Closing => "closing",
            LifecycleState::Closed => "closed",
        }
    }

    #[cfg(test)]
    pub(crate) fn identity(&self) -> usize {
        Arc::as_ptr(&self.shared) as usize
    }
}

impl Clone for SessionHandle {
    fn clone(&self) -> Self {
        // The explicit count represents application-visible leases only. The relaxed
        // increment needs no synchronization because an existing lease keeps the
        // allocation and its initialized fields alive during cloning.
        if self
            .shared
            .leases
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |leases| {
                leases.checked_add(1)
            })
            .is_err()
        {
            // Matching Arc's overflow policy avoids turning impossible adversarial
            // handle counts into a wrapped zero which could release a live session.
            std::process::abort();
        }
        Self {
            shared: Arc::clone(&self.shared),
        }
    }
}

impl Drop for SessionHandle {
    fn drop(&mut self) {
        // AcqRel makes the final releaser observe all preceding lease activity before
        // it attempts the independent lifecycle close election.
        if self.shared.leases.fetch_sub(1, Ordering::AcqRel) == 1 {
            self.shared.final_lease_dropped();
        }
    }
}

struct CloseCompletionGuard {
    session: Arc<SharedSession>,
    armed: bool,
}

impl CloseCompletionGuard {
    fn new(session: Arc<SharedSession>) -> Self {
        Self {
            session,
            armed: true,
        }
    }

    fn complete(&mut self, result: TerminalCloseResult) {
        if result.is_err() {
            self.session.abort_once();
        }
        self.session.publish_terminal(result);
        self.armed = false;
    }
}

impl Drop for CloseCompletionGuard {
    fn drop(&mut self) {
        if self.armed {
            // Cancellation or executor teardown must still leave a reusable terminal
            // result rather than marooning every later close waiter in Closing.
            self.session.abort_once();
            self.session.publish_terminal(Err(CloseError::Interrupted));
        }
    }
}
