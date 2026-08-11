//! Pure model of the fixed nested-session protocol probe.
//!
//! Generated code performs the real SDK calls. This module owns the smaller reference
//! model used to prove branch selection, error precedence, call isolation, and
//! cancellation without starting an engine or inventing a general module dispatcher.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};

/// Registration name carried by the sole private probe object.
pub const PROBE_OBJECT: &str = "RustSdkProtocolProbe";
/// Function name accepted by the invocation branch.
pub const PROBE_FUNCTION: &str = "probe";
/// Canonical JSON reported by a successful invocation.
pub const PROBE_RESULT_JSON: &str = "\"rust-sdk-protocol-ok\"";

/// Engine call branch selected from `FunctionCall.name`.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ProtocolBranch {
    /// An empty name registers the fixed object and function definition.
    Registration,
    /// The checked function name reports the fixed scalar value.
    Invocation,
}

/// Stable phase retained when the generated process cannot complete a call.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ProtocolFailurePhase {
    /// Existing-session metadata could not establish an authenticated client.
    Session,
    /// The current call was malformed or named an unsupported function.
    CallContext,
    /// The engine rejected module registration.
    Registration,
    /// The engine rejected the function result.
    Result,
    /// The operation succeeded but explicit client close failed.
    Close,
}

/// Failure switches used by deterministic protocol tests.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct ProtocolFailures {
    /// Connection fails before a client exists.
    pub connect: bool,
    /// Reading the current function name fails.
    pub call_context: bool,
    /// Serving registration fails.
    pub registration: bool,
    /// Reporting the scalar result fails.
    pub result: bool,
    /// Explicit close fails after the operation attempt.
    pub close: bool,
}

/// Successful branch observation from the reference model.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ProtocolSuccess {
    /// Selected fixed branch.
    pub branch: ProtocolBranch,
    /// Canonical result emitted only by invocation.
    pub result_json: Option<&'static str>,
}

/// Typed failure retaining primary operation precedence and a secondary close fact.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ProtocolFailure {
    /// Stable phase returned to the caller.
    pub primary: ProtocolFailurePhase,
    /// Close still ran after an operation failure and also failed.
    pub close_failed: bool,
}

/// Evaluates the same closed branch and close-precedence rules as generated code.
pub fn evaluate_protocol(
    function_name: &str,
    failures: ProtocolFailures,
) -> Result<ProtocolSuccess, ProtocolFailure> {
    if failures.connect {
        return Err(ProtocolFailure {
            primary: ProtocolFailurePhase::Session,
            close_failed: false,
        });
    }

    let operation = if failures.call_context {
        Err(ProtocolFailurePhase::CallContext)
    } else {
        match function_name {
            "" if failures.registration => Err(ProtocolFailurePhase::Registration),
            "" => Ok(ProtocolSuccess {
                branch: ProtocolBranch::Registration,
                result_json: None,
            }),
            PROBE_FUNCTION if failures.result => Err(ProtocolFailurePhase::Result),
            PROBE_FUNCTION => Ok(ProtocolSuccess {
                branch: ProtocolBranch::Invocation,
                result_json: Some(PROBE_RESULT_JSON),
            }),
            _ => Err(ProtocolFailurePhase::CallContext),
        }
    };

    match operation {
        Err(primary) => Err(ProtocolFailure {
            // Closing is best-effort after a primary operation error. Replacing the
            // source would hide the actionable registration or result failure.
            primary,
            close_failed: failures.close,
        }),
        Ok(_) if failures.close => Err(ProtocolFailure {
            primary: ProtocolFailurePhase::Close,
            close_failed: true,
        }),
        Ok(success) => Ok(success),
    }
}

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
