//! Shared deterministic foundations for Feature 2 integration and property tests.
//!
//! Recording doubles deliberately model only observable boundary calls. Production connection,
//! connector, and resource traits will be implemented on these values as those interfaces land;
//! keeping one event vocabulary prevents each lifecycle test from inventing incompatible fakes.

use std::sync::{Arc, Mutex};

use proptest::test_runner::{Config, FileFailurePersistence};

/// Required generated-case count for every Feature 2 property.
pub(crate) const PROPTEST_CASES: u32 = 256;

#[derive(Clone, Debug, Eq, PartialEq)]
/// Observable external-work or cleanup boundary reached by a test double.
pub(crate) enum RecordedAction {
    ConnectionExecute,
    ConnectionClose,
    ConnectionAbort,
    ConnectorConnect,
    ResourceClose,
}

#[derive(Clone, Default)]
/// Ordered event sink shared by the connection, connector, and resource doubles.
pub(crate) struct EventLog(Arc<Mutex<Vec<RecordedAction>>>);

impl EventLog {
    /// Records one boundary call in observation order.
    pub(crate) fn record(&self, action: RecordedAction) {
        // A poisoned test log means a prior assertion already unwound while holding the lock; using
        // its partial history would make the next assertion misleading rather than more robust.
        self.0
            .lock()
            .expect("recording fixture event log must not be poisoned")
            .push(action);
    }

    /// Returns a stable snapshot without exposing the synchronization primitive.
    pub(crate) fn actions(&self) -> Vec<RecordedAction> {
        self.0
            .lock()
            .expect("recording fixture event log must not be poisoned")
            .clone()
    }
}

#[derive(Clone)]
/// Recording double reserved for the stable EngineConnection boundary.
pub(crate) struct RecordingConnection(pub(crate) EventLog);

impl RecordingConnection {
    /// Records a request reaching the injected connection.
    pub(crate) fn execute(&self) {
        self.0.record(RecordedAction::ConnectionExecute);
    }

    /// Records graceful connection shutdown.
    pub(crate) fn close(&self) {
        self.0.record(RecordedAction::ConnectionClose);
    }

    /// Records the non-blocking connection backstop.
    pub(crate) fn abort(&self) {
        self.0.record(RecordedAction::ConnectionAbort);
    }
}

#[derive(Clone)]
/// Recording double reserved for the private connection-establishment boundary.
pub(crate) struct RecordingConnector(pub(crate) EventLog);

impl RecordingConnector {
    /// Records one attempt to establish a selected connection source.
    pub(crate) fn connect(&self) {
        self.0.record(RecordedAction::ConnectorConnect);
    }
}

#[derive(Clone)]
/// Recording double reserved for the session-resource cleanup boundary.
pub(crate) struct RecordingResource(pub(crate) EventLog);

impl RecordingResource {
    /// Records one graceful resource-close attempt.
    pub(crate) fn close(&self) {
        self.0.record(RecordedAction::ResourceClose);
    }
}

/// Returns the deterministic, persisted configuration shared by Feature 2 properties.
pub(crate) fn proptest_config() -> Config {
    Config {
        cases: PROPTEST_CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/client-lifecycle.txt"
        )))),
        ..Config::default()
    }
}
