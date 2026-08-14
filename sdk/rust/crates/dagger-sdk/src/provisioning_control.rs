//! Cooperative cancellation and deterministic checkpoints for CLI provisioning.
//!
//! Acquisition has no mutable global test controls. A provisioner owns one token and
//! a statically dispatched observer; production uses the no-op observer, while tests
//! can cancel at an exact boundary without changing URLs, clocks, or process state.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use tokio::sync::Notify;

use crate::provisioning_error::{ProvisionError, ProvisionErrorKind};

/// A cancellation boundary at which no completed artifact may be published afterward.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ProvisionCheckpoint {
    ManifestRequest,
    ManifestRead,
    ArchiveRequest,
    ArchiveRead,
    ChecksumAccepted,
    ExtractRead,
    Extracted,
    CacheLock,
    Flush,
    Publication,
}

/// A clonable, instance-local provisioning cancellation signal.
#[derive(Clone, Default)]
pub(crate) struct ProvisioningCancellation {
    inner: Arc<CancellationState>,
}

#[derive(Default)]
struct CancellationState {
    cancelled: AtomicBool,
    notify: Notify,
}

impl ProvisioningCancellation {
    pub(crate) fn new() -> Self {
        Self::default()
    }

    pub(crate) fn cancel(&self) {
        if !self.inner.cancelled.swap(true, Ordering::AcqRel) {
            self.inner.notify.notify_waiters();
        }
    }

    pub(crate) fn is_cancelled(&self) -> bool {
        self.inner.cancelled.load(Ordering::Acquire)
    }

    pub(crate) fn check(&self) -> Result<(), ProvisionError> {
        if self.is_cancelled() {
            Err(ProvisionError::new(ProvisionErrorKind::Cancelled))
        } else {
            Ok(())
        }
    }

    pub(crate) async fn cancelled(&self) {
        loop {
            if self.is_cancelled() {
                return;
            }
            let notified = self.inner.notify.notified();
            if self.is_cancelled() {
                return;
            }
            notified.await;
        }
    }
}

/// An instance-local observation seam used to make I/O schedules deterministic.
pub(crate) trait ProvisioningObserver: Send + Sync {
    fn checkpoint(&self, _checkpoint: ProvisionCheckpoint) {}

    #[cfg(feature = "signoff-observation")]
    fn manifest_available(&self, _sha256: [u8; 32]) {}

    #[cfg(feature = "signoff-observation")]
    fn manifest_unavailable(&self, _status: u16) {}
}

/// Production observer which adds no work to the provisioning path.
#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct NoopProvisioningObserver;

impl ProvisioningObserver for NoopProvisioningObserver {}

pub(crate) fn checkpoint<O: ProvisioningObserver>(
    observer: &O,
    cancellation: &ProvisioningCancellation,
    value: ProvisionCheckpoint,
) -> Result<(), ProvisionError> {
    observer.checkpoint(value);
    cancellation.check()
}
