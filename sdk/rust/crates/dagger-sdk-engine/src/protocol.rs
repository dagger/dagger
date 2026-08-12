//! Engine-free runtime-call isolation model.
//!
//! General registration, invocation, result election, and close precedence belong to
//! `dagger-sdk`'s production module adapter. This crate retains only the engine-owned
//! cloned-runtime isolation contract; it does not carry a second dispatch model.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};

/// One engine call's private execution inputs.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeCallInput {
    /// Unique engine call identity.
    pub call_id: String,
    /// Non-secret execution metadata visible to only this call.
    pub execution_metadata: String,
    /// Files written into this call's cloned runtime root.
    pub filesystem_writes: BTreeSet<String>,
    /// Scalar that would be published on success.
    pub result_json: String,
    /// Cancellation prevents result publication.
    pub cancelled: bool,
    /// An ordinary process failure also prevents result publication.
    pub failed: bool,
}

/// Isolated observation after one cloned runtime call terminates.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RuntimeCallObservation {
    /// Metadata observed by this call alone.
    pub execution_metadata: String,
    /// Files retained in this call's private clone.
    pub filesystem_writes: BTreeSet<String>,
    /// Published result, absent for rejection or cancellation.
    pub result_json: Option<String>,
    /// Every started process has reached a terminal reaped state.
    pub process_reaped: bool,
}

/// Models cloning one immutable runtime root for every engine call.
pub fn isolate_runtime_calls(
    calls: impl IntoIterator<Item = RuntimeCallInput>,
) -> Result<BTreeMap<String, RuntimeCallObservation>, EngineDiagnostic> {
    let mut observations = BTreeMap::new();
    for call in calls {
        let call_id = call.call_id.clone();
        let observation = RuntimeCallObservation {
            execution_metadata: call.execution_metadata,
            filesystem_writes: call.filesystem_writes,
            result_json: (!call.cancelled && !call.failed).then_some(call.result_json),
            // The engine does not return from ContainerRuntime.Call before Sync has
            // completed, so cancellation and failure cannot leave a child behind.
            process_reaped: true,
        };
        if observations.insert(call_id.clone(), observation).is_some() {
            return Err(EngineDiagnostic::new(
                EngineDiagnosticCode::RuntimeProtocolInvalid,
                Some("runtime.call-id"),
                "runtime call identities must be unique",
            ));
        }
    }
    Ok(observations)
}
