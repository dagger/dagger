//! Credential-free observations from the production implicit connector.
//!
//! This module exists only for exact-target SDK sign-off. The opt-in recorder follows one
//! [`crate::ClientConfig`] through preflight, CLI acquisition, authenticated loopback use, and
//! shutdown. Events deliberately contain only closed classifications, counts, and SHA-256
//! identities; session coordinates, credentials, headers, paths, and process output cannot cross
//! this boundary.

use std::error::Error;
use std::fmt;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{Receiver, SyncSender, TrySendError, sync_channel};

use crate::provisioning_control::{ProvisionCheckpoint, ProvisioningObserver};

/// One SHA-256 identity observed from exact executable or manifest bytes.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SignoffSha256([u8; 32]);

impl SignoffSha256 {
    /// Constructs an identity from the complete SHA-256 output.
    pub const fn from_bytes(bytes: [u8; 32]) -> Self {
        Self(bytes)
    }

    /// Returns the lowercase hexadecimal digest without a domain prefix.
    pub fn to_hex(self) -> String {
        hex::encode(self.0)
    }
}

/// The only release-manifest response classes which may authorize PATH compatibility.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SignoffUnavailableStatus {
    /// The immutable release endpoint returned HTTP 403.
    Forbidden,
    /// The immutable release endpoint returned HTTP 404.
    NotFound,
}

/// Production CLI source retained through one successful session launch.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SignoffCliSource {
    /// The checksum-verified compiled release downloaded into the isolated cache.
    VerifiedDownload,
    /// The exact artifact CLI selected after an admitted unavailable manifest response.
    CompatibilityPathFallback,
}

/// Credential-free fact emitted by one production connector instance.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SignoffConnectorEvent {
    /// Preflight selected compiled-release acquisition, excluding existing and explicit-local CLI.
    CompiledReleaseSelected,
    /// The distribution manifest was available and its complete bytes were decoded.
    ManifestAvailable {
        /// SHA-256 identity of the exact manifest bytes consumed by production.
        manifest_sha256: SignoffSha256,
    },
    /// The distribution manifest returned the exact compatibility response class.
    ManifestUnavailable {
        /// Closed HTTP response class consumed by production.
        status: SignoffUnavailableStatus,
    },
    /// Downloaded archive bytes matched the checksum from the consumed manifest.
    ArchiveChecksumVerified,
    /// Production committed to one executable before spawning the child.
    CliSelected {
        /// Mutually exclusive acquisition path selected by production.
        source: SignoffCliSource,
        /// SHA-256 identity read from the selected executable immediately before startup.
        executable_sha256: SignoffSha256,
    },
    /// The selected CLI child was successfully spawned.
    ChildStarted,
    /// The bounded control line yielded a valid port and non-empty token.
    SessionControlAccepted,
    /// The SDK constructed its proxy-free authenticated IPv4-loopback transport.
    AuthenticatedLoopbackConstructed,
    /// A request completed successfully through that authenticated transport.
    AuthenticatedQuerySucceeded,
    /// The shared client lifecycle elected the connector's sole graceful close.
    CloseStarted,
    /// The owned CLI child was reaped by the graceful shutdown path.
    ChildReaped,
    /// Transport, child, and background-worker shutdown all completed successfully.
    CloseCompleted,
}

const SIGNOFF_EVENT_CAPACITY: usize = 32;

/// Failure to retain a complete exact-target connector recording.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SignoffRecordingIncomplete;

impl fmt::Display for SignoffRecordingIncomplete {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("the bounded connector recording is incomplete")
    }
}

impl Error for SignoffRecordingIncomplete {}

/// Producer owned by one exact-target connector instance.
///
/// The producer accepts only the closed [`SignoffConnectorEvent`] vocabulary and never blocks
/// connector work. If its bounded queue cannot accept an event, the paired recording becomes
/// incomplete and therefore cannot support sign-off evidence.
#[derive(Clone)]
pub struct SignoffConnectorRecorder {
    sender: SyncSender<SignoffConnectorEvent>,
    incomplete: Arc<AtomicBool>,
}

/// Consumer retained by the exact-target runner until the connector has closed.
pub struct SignoffConnectorRecording {
    receiver: Receiver<SignoffConnectorEvent>,
    incomplete: Arc<AtomicBool>,
}

impl SignoffConnectorRecorder {
    /// Creates one bounded producer and its single consuming recording.
    pub fn bounded() -> (Self, SignoffConnectorRecording) {
        let (sender, receiver) = sync_channel(SIGNOFF_EVENT_CAPACITY);
        let incomplete = Arc::new(AtomicBool::new(false));
        (
            Self {
                sender,
                incomplete: incomplete.clone(),
            },
            SignoffConnectorRecording {
                receiver,
                incomplete,
            },
        )
    }

    fn record(&self, event: SignoffConnectorEvent) {
        if self.incomplete.load(Ordering::Acquire) {
            return;
        }
        if let Err(TrySendError::Full(_) | TrySendError::Disconnected(_)) =
            self.sender.try_send(event)
        {
            // Sign-off evidence must fail closed without ever imposing backpressure on the
            // production connector whose behavior it is observing.
            self.incomplete.store(true, Ordering::Release);
        }
    }
}

impl SignoffConnectorRecording {
    /// Consumes every retained event, failing if production could not retain the complete stream.
    pub fn finish(self) -> Result<Vec<SignoffConnectorEvent>, SignoffRecordingIncomplete> {
        let events = self.receiver.try_iter().collect();
        if self.incomplete.load(Ordering::Acquire) {
            Err(SignoffRecordingIncomplete)
        } else {
            Ok(events)
        }
    }
}

/// Instance-bound adapter from production boundaries to the closed recorder.
#[derive(Clone, Default)]
pub(crate) struct SignoffObservationDispatcher {
    recorder: Option<SignoffConnectorRecorder>,
}

impl SignoffObservationDispatcher {
    pub(crate) fn new(recorder: Option<SignoffConnectorRecorder>) -> Self {
        Self { recorder }
    }

    pub(crate) fn emit(&self, event: SignoffConnectorEvent) {
        if let Some(recorder) = &self.recorder {
            recorder.record(event);
        }
    }
}

impl ProvisioningObserver for SignoffObservationDispatcher {
    fn checkpoint(&self, checkpoint: ProvisionCheckpoint) {
        if checkpoint == ProvisionCheckpoint::ChecksumAccepted {
            self.emit(SignoffConnectorEvent::ArchiveChecksumVerified);
        }
    }

    fn manifest_available(&self, sha256: [u8; 32]) {
        self.emit(SignoffConnectorEvent::ManifestAvailable {
            manifest_sha256: SignoffSha256::from_bytes(sha256),
        });
    }

    fn manifest_unavailable(&self, status: u16) {
        let status = match status {
            403 => SignoffUnavailableStatus::Forbidden,
            404 => SignoffUnavailableStatus::NotFound,
            _ => return,
        };
        self.emit(SignoffConnectorEvent::ManifestUnavailable { status });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bounded_recorder_preserves_closed_event_order() {
        let (recorder, recording) = SignoffConnectorRecorder::bounded();
        let dispatcher = SignoffObservationDispatcher::new(Some(recorder));
        dispatcher.emit(SignoffConnectorEvent::CompiledReleaseSelected);
        dispatcher.emit(SignoffConnectorEvent::ChildStarted);
        assert_eq!(
            recording.finish().expect("recording remains complete"),
            [
                SignoffConnectorEvent::CompiledReleaseSelected,
                SignoffConnectorEvent::ChildStarted,
            ]
        );
    }

    #[test]
    fn overflow_invalidates_the_complete_recording_without_blocking() {
        let (recorder, recording) = SignoffConnectorRecorder::bounded();
        let dispatcher = SignoffObservationDispatcher::new(Some(recorder));
        for _ in 0..=SIGNOFF_EVENT_CAPACITY {
            dispatcher.emit(SignoffConnectorEvent::ChildStarted);
        }
        assert_eq!(recording.finish(), Err(SignoffRecordingIncomplete));
    }

    #[test]
    fn digest_rendering_is_closed_and_lowercase() {
        assert_eq!(
            SignoffSha256::from_bytes([0xab; 32]).to_hex(),
            "ab".repeat(32)
        );
    }
}
