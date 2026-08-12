//! Total call preparation, typed dispatch, and single-winner result publication.
//!
//! Validation consumes the complete named argument list before a generated bridge can
//! run. Dispatch then races only call-local cancellation against user execution and
//! sink acceptance; no process-global registry, result, or cancellation state exists.

use std::collections::{BTreeMap, BTreeSet};
use std::error::Error;
use std::fmt;
use std::panic::AssertUnwindSafe;
use std::sync::Arc;
use std::sync::atomic::{AtomicU8, Ordering};

use futures::FutureExt;

use super::codec::__private::ModuleBoxFuture;
use super::context::ModuleContextBase;
use super::error::ModuleError;
use super::view::{ModuleDescriptorView, RegistrationView};
use super::wire::{CallEnvelope, CallIdentity, CallSelector, ModuleJson};
use crate::CloseError;

/// Stable parent/function coordinates retained by every dispatch failure.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CallCoordinate {
    /// Selected parent wire name.
    pub parent_wire_name: String,
    /// Selected function wire name.
    pub function_wire_name: String,
}

impl CallCoordinate {
    fn from_selector(selector: &CallSelector) -> Option<Self> {
        let CallSelector::Invocation {
            parent_wire_name,
            function_wire_name,
        } = selector
        else {
            return None;
        };
        Some(Self {
            parent_wire_name: parent_wire_name.as_str().to_owned(),
            function_wire_name: function_wire_name.as_str().to_owned(),
        })
    }
}

impl fmt::Display for CallCoordinate {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{}.{}",
            self.parent_wire_name, self.function_wire_name
        )
    }
}

/// Validated call inputs owned by one generated dispatch arm.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PreparedCall {
    /// Stable call identity and selected coordinate.
    pub identity: CallIdentity,
    /// Canonical parent value retained for receiver decoding.
    pub parent: Option<ModuleJson>,
    /// Arguments ordered by the generated function descriptor.
    pub arguments: Vec<ModuleJson>,
}

/// Total pre-execution call validation failure.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CallPreparationError {
    /// Registration was passed to the invocation preparation path.
    Registration,
    /// The selected parent does not occur in the closed descriptor.
    UnknownParent(CallCoordinate),
    /// The selected function does not occur below the known parent.
    UnknownFunction(CallCoordinate),
    /// A constructor call supplied receiver state.
    UnexpectedParent(CallCoordinate),
    /// An instance call omitted receiver state.
    MissingParent(CallCoordinate),
    /// An instance receiver was not a canonical object state value.
    MalformedParent(CallCoordinate),
    /// A required named argument was omitted.
    MissingArgument {
        coordinate: CallCoordinate,
        argument_wire_name: String,
    },
    /// One named argument occurred more than once.
    DuplicateArgument {
        coordinate: CallCoordinate,
        argument_wire_name: String,
    },
    /// The call supplied a name absent from the selected descriptor entry.
    UnknownArgument {
        coordinate: CallCoordinate,
        argument_wire_name: String,
    },
    /// Generated canonical default JSON was internally inconsistent.
    InvalidDefault {
        coordinate: CallCoordinate,
        argument_wire_name: String,
    },
}

impl fmt::Display for CallPreparationError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Registration => formatter.write_str("registration is not an invocation"),
            Self::UnknownParent(_) => formatter.write_str("module dispatch parent is unknown"),
            Self::UnknownFunction(_) => formatter.write_str("module dispatch function is unknown"),
            Self::UnexpectedParent(_) => {
                formatter.write_str("a module constructor received parent state")
            }
            Self::MissingParent(_) => {
                formatter.write_str("a module instance function requires parent state")
            }
            Self::MalformedParent(_) => {
                formatter.write_str("module parent state has an invalid shape")
            }
            Self::MissingArgument { .. } => {
                formatter.write_str("a required module argument is missing")
            }
            Self::DuplicateArgument { .. } => {
                formatter.write_str("a module argument was supplied more than once")
            }
            Self::UnknownArgument { .. } => formatter.write_str("a module argument is unknown"),
            Self::InvalidDefault { .. } => {
                formatter.write_str("a generated module argument default is invalid")
            }
        }
    }
}

impl Error for CallPreparationError {}

/// Validates and orders one invocation before any authored bridge can execute.
#[doc(hidden)]
pub fn prepare_call(
    descriptor: &'static ModuleDescriptorView,
    envelope: CallEnvelope,
) -> Result<PreparedCall, CallPreparationError> {
    let Some(coordinate) = CallCoordinate::from_selector(envelope.identity.selector()) else {
        return Err(CallPreparationError::Registration);
    };
    let parent_exists = descriptor
        .functions
        .iter()
        .any(|function| function.parent_wire_name == coordinate.parent_wire_name);
    if !parent_exists {
        return Err(CallPreparationError::UnknownParent(coordinate));
    }
    let function = descriptor
        .functions
        .iter()
        .find(|function| {
            function.parent_wire_name == coordinate.parent_wire_name
                && function.function_wire_name == coordinate.function_wire_name
        })
        .ok_or_else(|| CallPreparationError::UnknownFunction(coordinate.clone()))?;

    match (function.constructor, envelope.parent.as_ref()) {
        (true, Some(_)) => {
            return Err(CallPreparationError::UnexpectedParent(coordinate));
        }
        (false, None) => return Err(CallPreparationError::MissingParent(coordinate)),
        (false, Some(parent)) if !parent.as_json().is_object() => {
            return Err(CallPreparationError::MalformedParent(coordinate));
        }
        _ => {}
    }

    let expected = function
        .arguments
        .iter()
        .map(|argument| argument.wire_name)
        .collect::<BTreeSet<_>>();
    let mut supplied = BTreeMap::new();
    let mut duplicate = None;
    for argument in envelope.arguments {
        let name = argument.name.as_str().to_owned();
        if supplied.insert(name.clone(), argument.value).is_some() && duplicate.is_none() {
            duplicate = Some(name);
        }
    }
    if let Some(argument_wire_name) = duplicate {
        return Err(CallPreparationError::DuplicateArgument {
            coordinate,
            argument_wire_name,
        });
    }
    if let Some(argument_wire_name) = supplied
        .keys()
        .find(|name| !expected.contains(name.as_str()))
        .cloned()
    {
        return Err(CallPreparationError::UnknownArgument {
            coordinate,
            argument_wire_name,
        });
    }

    let mut arguments = Vec::with_capacity(function.arguments.len());
    for argument in function.arguments {
        if let Some(value) = supplied.remove(argument.wire_name) {
            arguments.push(value);
        } else if let Some(default_json) = argument.default_json {
            let value = serde_json::from_str(default_json).map_err(|_| {
                CallPreparationError::InvalidDefault {
                    coordinate: coordinate.clone(),
                    argument_wire_name: argument.wire_name.to_owned(),
                }
            })?;
            arguments.push(ModuleJson::new(value));
        } else if argument.required {
            return Err(CallPreparationError::MissingArgument {
                coordinate,
                argument_wire_name: argument.wire_name.to_owned(),
            });
        } else {
            arguments.push(ModuleJson::new(serde_json::Value::Null));
        }
    }

    Ok(PreparedCall {
        identity: envelope.identity,
        parent: envelope.parent,
        arguments,
    })
}

/// Error outcome provenance retained without exposing panic payloads.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ErrorOutcomeKind {
    /// Intentional authored application error.
    Application,
    /// Contained unwind from the authored user future.
    Panic,
}

/// One terminal outcome selected before publication.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CallOutcome {
    /// Canonically encoded successful value.
    Value(ModuleJson),
    /// Structured error with credential-safe provenance.
    Error {
        error: ModuleError,
        kind: ErrorOutcomeKind,
    },
}

/// Terminal kind retained by a successful publication receipt.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum PublishedOutcome {
    /// A successful encoded value was accepted.
    Value,
    /// An intentional authored error was accepted.
    ApplicationError,
    /// A contained panic error was accepted.
    PanicError,
}

impl CallOutcome {
    fn published_kind(&self) -> PublishedOutcome {
        match self {
            Self::Value(_) => PublishedOutcome::Value,
            Self::Error {
                kind: ErrorOutcomeKind::Application,
                ..
            } => PublishedOutcome::ApplicationError,
            Self::Error {
                kind: ErrorOutcomeKind::Panic,
                ..
            } => PublishedOutcome::PanicError,
        }
    }
}

macro_rules! adapter_error {
    ($name:ident, $message:literal) => {
        #[doc = $message]
        #[derive(Clone)]
        pub struct $name {
            source: Option<Arc<dyn Error + Send + Sync + 'static>>,
        }

        impl $name {
            /// Constructs an adapter failure without an opaque cause.
            #[must_use]
            pub const fn new() -> Self {
                Self { source: None }
            }

            /// Retains the transport or query cause without rendering it ordinarily.
            pub fn with_source<E>(source: E) -> Self
            where
                E: Error + Send + Sync + 'static,
            {
                Self {
                    source: Some(Arc::new(source)),
                }
            }
        }

        impl Default for $name {
            fn default() -> Self {
                Self::new()
            }
        }

        impl fmt::Debug for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter
                    .debug_struct(stringify!($name))
                    .field("source_present", &self.source.is_some())
                    .finish_non_exhaustive()
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str($message)
            }
        }

        impl Error for $name {
            fn source(&self) -> Option<&(dyn Error + 'static)> {
                self.source
                    .as_deref()
                    .map(|source| source as &(dyn Error + 'static))
            }
        }
    };
}

adapter_error!(RegistrationError, "module registration publication failed");
adapter_error!(ResultPublishError, "module result publication failed");

/// Typed failure from one generated registry arm.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum InvocationError {
    /// Receiver state failed its exact generated codec.
    ParentDecode(CallCoordinate),
    /// One ordered argument failed its exact generated codec.
    ArgumentDecode {
        coordinate: CallCoordinate,
        argument_wire_name: String,
    },
    /// Authored code returned an intentional application error.
    Application(ModuleError),
    /// Successful value encoding or handle-ID resolution failed.
    Encode {
        coordinate: CallCoordinate,
        result_type: String,
    },
    /// The parent coordinate is absent from the closed registry.
    UnknownParent,
    /// The function coordinate is absent below a known parent.
    UnknownFunction,
}

impl fmt::Display for InvocationError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::ParentDecode(_) => "generated module parent decoding failed",
            Self::ArgumentDecode { .. } => "generated module argument decoding failed",
            Self::Application(_) => "module application returned an error",
            Self::Encode { .. } => "generated module output encoding failed",
            Self::UnknownParent => "module dispatch parent is unknown",
            Self::UnknownFunction => "module dispatch function is unknown",
        })
    }
}

impl Error for InvocationError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Application(error) => Some(error),
            _ => None,
        }
    }
}

/// Closed generated dispatch registry.
#[doc(hidden)]
pub trait DispatchRegistry: Send + Sync {
    /// Returns the descriptor view compiled into this registry.
    fn descriptor(&self) -> &'static ModuleDescriptorView;

    /// Returns the registration projection generated from the same descriptor.
    fn registration(&self) -> &'static RegistrationView;

    /// Invokes one already-selected and validated typed adapter.
    fn invoke<'a>(
        &'a self,
        call: PreparedCall,
        context: ModuleContextBase,
    ) -> ModuleBoxFuture<'a, Result<ModuleJson, InvocationError>>;
}

/// Sink for one complete descriptor-derived registration projection.
#[doc(hidden)]
pub trait RegistrationSink: Send + Sync {
    /// Serves the complete registration projection exactly once.
    fn serve<'a>(
        &'a self,
        registration: &'a RegistrationView,
    ) -> ModuleBoxFuture<'a, Result<(), RegistrationError>>;
}

/// Sink for one selected terminal invocation outcome.
#[doc(hidden)]
pub trait ResultSink: Send + Sync {
    /// Publishes one terminal outcome without retry or fallback.
    fn publish<'a>(
        &'a self,
        outcome: CallOutcome,
    ) -> ModuleBoxFuture<'a, Result<(), ResultPublishError>>;
}

/// Successful registration or invocation handling receipt.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CallReceipt {
    /// Stable call identity accepted by the selected sink.
    pub identity: CallIdentity,
    /// Published invocation kind, or `None` for registration.
    pub outcome: Option<PublishedOutcome>,
}

/// Typed terminal failure from the generic production dispatcher.
#[doc(hidden)]
#[derive(Debug)]
pub enum DispatchError {
    /// Call preparation rejected the complete envelope.
    Preparation(CallPreparationError),
    /// The supplied context does not belong to the envelope.
    ContextMismatch,
    /// Generated decoding or encoding failed before publication.
    Invocation(InvocationError),
    /// Call-local cancellation won before sink acceptance.
    Cancelled(CallCoordinate),
    /// A contained user panic was published without exposing its payload.
    Panicked(CallCoordinate),
    /// The selected result sink rejected its one publication attempt.
    Publication {
        coordinate: CallCoordinate,
        outcome: PublishedOutcome,
        source: ResultPublishError,
    },
    /// Complete registration publication failed.
    Registration(RegistrationError),
    /// Entrypoint close failed after an otherwise successful operation.
    Close(CloseError),
}

impl fmt::Display for DispatchError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Preparation(_) => "module call preparation failed",
            Self::ContextMismatch => "module call context does not match its envelope",
            Self::Invocation(_) => "module invocation failed",
            Self::Cancelled(_) => "module invocation was cancelled",
            Self::Panicked(_) => "module function panicked",
            Self::Publication { .. } => "module result publication failed",
            Self::Registration(_) => "module registration publication failed",
            Self::Close(_) => "module session close failed",
        })
    }
}

impl Error for DispatchError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Preparation(error) => Some(error),
            Self::Invocation(error) => Some(error),
            Self::Publication { source, .. } => Some(source),
            Self::Registration(error) => Some(error),
            Self::Close(error) => Some(error),
            Self::ContextMismatch | Self::Cancelled(_) | Self::Panicked(_) => None,
        }
    }
}

/// Observable state of one call-local result election.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ResultElectionState {
    /// Neither cancellation nor publication has started.
    Pending = 0,
    /// One outcome is being offered to the sink.
    Publishing = 1,
    /// The sink accepted the selected outcome.
    Published = 2,
    /// Cancellation won before sink acceptance.
    Cancelled = 3,
    /// The sink rejected the selected outcome.
    PublicationFailed = 4,
}

impl ResultElectionState {
    fn from_raw(value: u8) -> Self {
        match value {
            0 => Self::Pending,
            1 => Self::Publishing,
            2 => Self::Published,
            3 => Self::Cancelled,
            _ => Self::PublicationFailed,
        }
    }
}

/// Cloneable call-local state machine with one terminal winner.
#[doc(hidden)]
#[derive(Clone, Debug)]
pub struct ResultElection(Arc<AtomicU8>);

impl Default for ResultElection {
    fn default() -> Self {
        Self(Arc::new(AtomicU8::new(ResultElectionState::Pending as u8)))
    }
}

impl ResultElection {
    /// Returns the current monotonic election state.
    #[must_use]
    pub fn state(&self) -> ResultElectionState {
        ResultElectionState::from_raw(self.0.load(Ordering::Acquire))
    }

    /// Moves a pending election into its one publication attempt.
    #[doc(hidden)]
    pub fn begin_publication(&self) -> bool {
        self.0
            .compare_exchange(
                ResultElectionState::Pending as u8,
                ResultElectionState::Publishing as u8,
                Ordering::AcqRel,
                Ordering::Acquire,
            )
            .is_ok()
    }

    /// Records sink acceptance if cancellation has not already won.
    #[doc(hidden)]
    pub fn accept_publication(&self) -> bool {
        self.0
            .compare_exchange(
                ResultElectionState::Publishing as u8,
                ResultElectionState::Published as u8,
                Ordering::AcqRel,
                Ordering::Acquire,
            )
            .is_ok()
    }

    /// Records terminal rejection of the selected sink outcome.
    #[doc(hidden)]
    pub fn fail_publication(&self) {
        let _ = self.0.compare_exchange(
            ResultElectionState::Publishing as u8,
            ResultElectionState::PublicationFailed as u8,
            Ordering::AcqRel,
            Ordering::Acquire,
        );
    }

    /// Elects cancellation while publication remains unaccepted.
    #[doc(hidden)]
    pub fn cancel(&self) -> bool {
        let mut observed = self.0.load(Ordering::Acquire);
        loop {
            if !matches!(
                ResultElectionState::from_raw(observed),
                ResultElectionState::Pending | ResultElectionState::Publishing
            ) {
                return false;
            }
            match self.0.compare_exchange_weak(
                observed,
                ResultElectionState::Cancelled as u8,
                Ordering::AcqRel,
                Ordering::Acquire,
            ) {
                Ok(_) => return true,
                Err(next) => observed = next,
            }
        }
    }
}

/// Routes registration and invocation through the same production wrapper.
#[doc(hidden)]
pub async fn handle_call<R, G, S>(
    registry: &R,
    envelope: CallEnvelope,
    context: ModuleContextBase,
    registration_sink: &G,
    result_sink: &S,
) -> Result<CallReceipt, DispatchError>
where
    R: DispatchRegistry,
    G: RegistrationSink,
    S: ResultSink,
{
    if !context_matches(&envelope, &context) {
        return Err(DispatchError::ContextMismatch);
    }
    match envelope.identity.selector() {
        CallSelector::Registration => {
            registration_sink
                .serve(registry.registration())
                .await
                .map_err(DispatchError::Registration)?;
            Ok(CallReceipt {
                identity: envelope.identity,
                outcome: None,
            })
        }
        CallSelector::Invocation { .. } => dispatch(registry, envelope, context, result_sink).await,
    }
}

/// Validates, invokes, and publishes exactly one call-local terminal outcome.
#[doc(hidden)]
pub async fn dispatch<R, S>(
    registry: &R,
    envelope: CallEnvelope,
    context: ModuleContextBase,
    sink: &S,
) -> Result<CallReceipt, DispatchError>
where
    R: DispatchRegistry,
    S: ResultSink,
{
    if !context_matches(&envelope, &context) {
        return Err(DispatchError::ContextMismatch);
    }
    let call = prepare_call(registry.descriptor(), envelope).map_err(DispatchError::Preparation)?;
    let identity = call.identity.clone();
    let coordinate = CallCoordinate::from_selector(identity.selector()).ok_or(
        DispatchError::Preparation(CallPreparationError::Registration),
    )?;
    let cancellation = context.cancellation().clone();
    if cancellation.is_cancelled() {
        return Err(DispatchError::Cancelled(coordinate));
    }
    let invocation = AssertUnwindSafe(registry.invoke(call, context)).catch_unwind();
    tokio::pin!(invocation);

    let outcome = tokio::select! {
        _ = cancellation.cancelled() => return Err(DispatchError::Cancelled(coordinate)),
        result = &mut invocation => match result {
            Ok(Ok(value)) => CallOutcome::Value(value),
            Ok(Err(InvocationError::Application(error))) => CallOutcome::Error {
                error,
                kind: ErrorOutcomeKind::Application,
            },
            Ok(Err(error)) => return Err(DispatchError::Invocation(error)),
            Err(_) => CallOutcome::Error {
                error: ModuleError::new("module function panicked"),
                kind: ErrorOutcomeKind::Panic,
            },
        }
    };

    let published = outcome.published_kind();
    let election = ResultElection::default();
    if !election.begin_publication() {
        return Err(DispatchError::Cancelled(coordinate));
    }
    let publication = sink.publish(outcome);
    tokio::pin!(publication);
    tokio::select! {
        _ = cancellation.cancelled() => {
            if election.cancel() {
                Err(DispatchError::Cancelled(coordinate))
            } else {
                Ok(CallReceipt { identity, outcome: Some(published) })
            }
        }
        result = &mut publication => match result {
            Ok(()) => {
                if election.accept_publication() {
                    if published == PublishedOutcome::PanicError {
                        Err(DispatchError::Panicked(coordinate))
                    } else {
                        Ok(CallReceipt { identity, outcome: Some(published) })
                    }
                } else {
                    Err(DispatchError::Cancelled(coordinate))
                }
            }
            Err(source) => {
                election.fail_publication();
                Err(DispatchError::Publication {
                    coordinate,
                    outcome: published,
                    source,
                })
            }
        }
    }
}

fn context_matches(envelope: &CallEnvelope, context: &ModuleContextBase) -> bool {
    context.current_call().id() == envelope.identity.call_id()
        && context.current_call().selector() == envelope.identity.selector()
}

/// Applies entrypoint close without masking an earlier operation failure.
#[doc(hidden)]
pub fn apply_close_precedence<T>(
    operation: Result<T, DispatchError>,
    close: Result<(), CloseError>,
) -> Result<T, DispatchError> {
    match operation {
        Err(primary) => Err(primary),
        Ok(value) => close.map(|()| value).map_err(DispatchError::Close),
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::{Arc, Mutex};

    use async_trait::async_trait;
    use proptest::prelude::*;
    use serde_json::json;

    use super::*;
    use crate::connection::{EngineConnection, EngineConnectionError};
    use crate::graphql::{RawRequest, RawResponse, ResponseData};
    use crate::lifecycle::SessionHandle;
    use crate::module::context::{CurrentCall, ModuleCancellation};
    use crate::module::view::{ArgumentView, FunctionView};
    use crate::module::wire::{ModuleFunctionName, ModuleWireName, NamedModuleArgument};
    use crate::query::QueryBuilder;
    use crate::test_support::{io_proptest_config, proptest_config};

    static ARGUMENTS: &[ArgumentView] = &[
        ArgumentView {
            wire_name: "name",
            required: true,
            default_json: None,
        },
        ArgumentView {
            wire_name: "count",
            required: false,
            default_json: Some("7"),
        },
        ArgumentView {
            wire_name: "enabled",
            required: false,
            default_json: Some("true"),
        },
    ];
    static FUNCTIONS: &[FunctionView] = &[
        FunctionView {
            parent_wire_name: "Fixture",
            function_wire_name: "",
            constructor: true,
            arguments: &[],
            result_type: "Fixture",
        },
        FunctionView {
            parent_wire_name: "Fixture",
            function_wire_name: "run",
            constructor: false,
            arguments: ARGUMENTS,
            result_type: "String",
        },
    ];
    static DESCRIPTOR: ModuleDescriptorView = ModuleDescriptorView {
        digest: "sha256:fixture",
        root_wire_name: "Fixture",
        types: &[],
        functions: FUNCTIONS,
    };
    static REGISTRATION: RegistrationView = RegistrationView {
        descriptor_digest: "sha256:fixture",
        canonical_json: "{}",
    };

    struct CountingConnection(Arc<AtomicUsize>);

    #[async_trait]
    impl EngineConnection for CountingConnection {
        async fn execute(
            &self,
            _request: RawRequest,
        ) -> Result<RawResponse, EngineConnectionError> {
            Ok(RawResponse::new(ResponseData::Null))
        }

        async fn close(&self) -> Result<(), EngineConnectionError> {
            self.0.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }

        fn abort(&self) {
            self.0.fetch_add(1, Ordering::SeqCst);
        }
    }

    #[derive(Clone, Copy)]
    enum Behavior {
        Value,
        Application,
        EncodeFailure,
        Panic,
        PerCall,
    }

    #[derive(Clone, Debug, Eq, PartialEq)]
    struct TelemetryMarker(String);

    struct Registry {
        behavior: Behavior,
        user_events: Arc<AtomicUsize>,
        barrier: Option<Arc<tokio::sync::Barrier>>,
    }

    impl DispatchRegistry for Registry {
        fn descriptor(&self) -> &'static ModuleDescriptorView {
            &DESCRIPTOR
        }

        fn registration(&self) -> &'static RegistrationView {
            &REGISTRATION
        }

        fn invoke<'a>(
            &'a self,
            call: PreparedCall,
            context: ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, InvocationError>> {
            Box::pin(async move {
                let coordinate = CallCoordinate::from_selector(call.identity.selector())
                    .expect("fixture calls are invocations");
                let constructor = coordinate.function_wire_name.is_empty();
                let name = if constructor {
                    "constructed"
                } else {
                    let Some(name) = call
                        .arguments
                        .first()
                        .and_then(|value| value.as_json().as_str())
                    else {
                        return Err(InvocationError::ArgumentDecode {
                            coordinate,
                            argument_wire_name: "name".to_owned(),
                        });
                    };
                    name
                };
                if let Some(barrier) = &self.barrier {
                    barrier.wait().await;
                }
                if matches!(self.behavior, Behavior::PerCall)
                    && context
                        .telemetry_context()
                        .get::<TelemetryMarker>()
                        .is_none_or(|marker| marker.0 != call.identity.call_id())
                {
                    return Err(InvocationError::ParentDecode(coordinate));
                }
                self.user_events.fetch_add(1, Ordering::SeqCst);
                match self.behavior {
                    Behavior::Value => Ok(ModuleJson::new(json!({
                        "count": call.arguments.get(1).map(ModuleJson::as_json),
                        "enabled": call.arguments.get(2).map(ModuleJson::as_json),
                        "name": name,
                    }))),
                    Behavior::Application => {
                        Err(InvocationError::Application(ModuleError::new("rejected")))
                    }
                    Behavior::EncodeFailure => Err(InvocationError::Encode {
                        coordinate,
                        result_type: "String".to_owned(),
                    }),
                    Behavior::Panic => panic!("secret panic payload"),
                    Behavior::PerCall if call.identity.call_id().contains("panic") => {
                        panic!("isolated panic payload")
                    }
                    Behavior::PerCall => {
                        let fixture_root = call
                            .parent
                            .as_ref()
                            .and_then(|parent| parent.as_json().get("fixture_root"))
                            .and_then(serde_json::Value::as_str)
                            .ok_or(InvocationError::ParentDecode(coordinate))?;
                        Ok(ModuleJson::new(json!({
                            "call_id": call.identity.call_id(),
                            "fixture_root": fixture_root,
                            "name": name,
                        })))
                    }
                }
            })
        }
    }

    struct Sink {
        outcomes: Arc<Mutex<Vec<CallOutcome>>>,
        fail: bool,
    }

    impl ResultSink for Sink {
        fn publish<'a>(
            &'a self,
            outcome: CallOutcome,
        ) -> ModuleBoxFuture<'a, Result<(), ResultPublishError>> {
            Box::pin(async move {
                if self.fail {
                    Err(ResultPublishError::new())
                } else {
                    self.outcomes.lock().expect("outcome lock").push(outcome);
                    Ok(())
                }
            })
        }
    }

    struct RegistrationRecorder(Arc<AtomicUsize>);

    impl RegistrationSink for RegistrationRecorder {
        fn serve<'a>(
            &'a self,
            registration: &'a RegistrationView,
        ) -> ModuleBoxFuture<'a, Result<(), RegistrationError>> {
            Box::pin(async move {
                assert_eq!(registration, &REGISTRATION);
                self.0.fetch_add(1, Ordering::SeqCst);
                Ok(())
            })
        }
    }

    struct FailingRegistrationRecorder {
        events: Arc<AtomicUsize>,
        fail: bool,
    }

    impl RegistrationSink for FailingRegistrationRecorder {
        fn serve<'a>(
            &'a self,
            registration: &'a RegistrationView,
        ) -> ModuleBoxFuture<'a, Result<(), RegistrationError>> {
            Box::pin(async move {
                assert_eq!(registration, &REGISTRATION);
                self.events.fetch_add(1, Ordering::SeqCst);
                if self.fail {
                    Err(RegistrationError::new())
                } else {
                    Ok(())
                }
            })
        }
    }

    fn selector() -> CallSelector {
        CallSelector::Invocation {
            parent_wire_name: ModuleWireName::new("Fixture").expect("static name is valid"),
            function_wire_name: ModuleFunctionName::new("run").expect("static name is valid"),
        }
    }

    fn argument(name: &str, value: serde_json::Value) -> NamedModuleArgument {
        NamedModuleArgument {
            name: ModuleWireName::new(name).expect("fixture argument name is valid"),
            value: ModuleJson::new(value),
        }
    }

    fn envelope(
        call_id: &str,
        parent: Option<serde_json::Value>,
        arguments: Vec<NamedModuleArgument>,
    ) -> CallEnvelope {
        CallEnvelope::new(
            CallIdentity::new(call_id, selector()).expect("fixture identity is valid"),
            parent.map(ModuleJson::new),
            arguments,
        )
    }

    fn context(
        call_id: &str,
        cancellation: ModuleCancellation,
    ) -> (ModuleContextBase, Arc<AtomicUsize>) {
        let aborts = Arc::new(AtomicUsize::new(0));
        let builder = QueryBuilder::new(SessionHandle::new(
            Box::new(CountingConnection(Arc::clone(&aborts))),
            None,
            None,
        ));
        (
            ModuleContextBase::new(
                builder,
                cancellation,
                opentelemetry::Context::new().with_value(TelemetryMarker(call_id.to_owned())),
                CurrentCall::new(call_id, selector()).expect("fixture identity is valid"),
            ),
            aborts,
        )
    }

    #[test]
    fn adapter_errors_retain_sources_without_rendering_them() {
        #[derive(Debug)]
        struct TransportSource;

        impl fmt::Display for TransportSource {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str("credential-bearing transport detail")
            }
        }

        impl Error for TransportSource {}

        let error = ResultPublishError::with_source(TransportSource);
        assert_eq!(error.to_string(), "module result publication failed");
        assert!(error.source().is_some());
        assert!(!error.to_string().contains("credential"));
    }

    #[tokio::test]
    async fn registration_uses_the_descriptor_owned_projection_once() {
        let registry = Registry {
            behavior: Behavior::Value,
            user_events: Arc::new(AtomicUsize::new(0)),
            barrier: None,
        };
        let identity = CallIdentity::new("registration", CallSelector::Registration)
            .expect("static identity is valid");
        let envelope = CallEnvelope::new(identity.clone(), None, Vec::new());
        let aborts = Arc::new(AtomicUsize::new(0));
        let context = ModuleContextBase::new(
            QueryBuilder::new(SessionHandle::new(
                Box::new(CountingConnection(aborts)),
                None,
                None,
            )),
            ModuleCancellation::default(),
            opentelemetry::Context::new(),
            CurrentCall::new("registration", CallSelector::Registration)
                .expect("static identity is valid"),
        );
        let registrations = Arc::new(AtomicUsize::new(0));
        let registration_sink = RegistrationRecorder(Arc::clone(&registrations));
        let outcomes = Arc::new(Mutex::new(Vec::new()));
        let result_sink = Sink {
            outcomes: Arc::clone(&outcomes),
            fail: false,
        };

        let receipt = handle_call(
            &registry,
            envelope,
            context,
            &registration_sink,
            &result_sink,
        )
        .await
        .expect("registration publishes");
        assert_eq!(receipt.identity, identity);
        assert_eq!(receipt.outcome, None);
        assert_eq!(registrations.load(Ordering::SeqCst), 1);
        assert!(outcomes.lock().expect("outcome lock").is_empty());
    }

    proptest! {
        #![proptest_config(proptest_config())]

        #[test]
        fn property_14_registration_invocation_branches_disjoint(
            registration in any::<bool>(),
            constructor in any::<bool>(),
            registration_fails in any::<bool>(),
            publication_fails in any::<bool>(),
            call_suffix in any::<u64>(),
        ) {
            let selector = if registration {
                CallSelector::Registration
            } else if constructor {
                CallSelector::Invocation {
                    parent_wire_name: ModuleWireName::new("Fixture").expect("static name is valid"),
                    function_wire_name: ModuleFunctionName::new("").expect("constructor name is valid"),
                }
            } else {
                selector()
            };
            let call_id = format!("branch-{call_suffix}");
            let identity = CallIdentity::new(&call_id, selector.clone()).expect("generated identity is bounded");
            let envelope = if registration || constructor {
                CallEnvelope::new(identity, None, Vec::new())
            } else {
                CallEnvelope::new(
                    identity,
                    Some(ModuleJson::new(json!({}))),
                    vec![argument("name", json!("value"))],
                )
            };
            let aborts = Arc::new(AtomicUsize::new(0));
            let context = ModuleContextBase::new(
                QueryBuilder::new(SessionHandle::new(
                    Box::new(CountingConnection(aborts)),
                    None,
                    None,
                )),
                ModuleCancellation::default(),
                opentelemetry::Context::new(),
                CurrentCall::new(&call_id, selector).expect("generated current call is bounded"),
            );
            let user_events = Arc::new(AtomicUsize::new(0));
            let registry = Registry {
                behavior: Behavior::Value,
                user_events: Arc::clone(&user_events),
                barrier: None,
            };
            let registration_events = Arc::new(AtomicUsize::new(0));
            let registration_sink = FailingRegistrationRecorder {
                events: Arc::clone(&registration_events),
                fail: registration_fails,
            };
            let outcomes = Arc::new(Mutex::new(Vec::new()));
            let result_sink = Sink {
                outcomes: Arc::clone(&outcomes),
                fail: publication_fails,
            };

            let result = futures::executor::block_on(handle_call(
                &registry,
                envelope,
                context,
                &registration_sink,
                &result_sink,
            ));

            if registration {
                prop_assert_eq!(registration_events.load(Ordering::SeqCst), 1);
                prop_assert_eq!(user_events.load(Ordering::SeqCst), 0);
                prop_assert!(outcomes.lock().expect("outcome lock").is_empty());
                prop_assert_eq!(result.is_err(), registration_fails);
            } else {
                prop_assert_eq!(registration_events.load(Ordering::SeqCst), 0);
                prop_assert_eq!(user_events.load(Ordering::SeqCst), 1);
                let published = outcomes.lock().expect("outcome lock").len();
                prop_assert_eq!(published, usize::from(!publication_fails));
                prop_assert_eq!(result.is_err(), publication_fails);
            }
        }

        #[test]
        fn property_15_parent_argument_validation_precedes_execution(
            mode in 0_u8..7,
            reverse in any::<bool>(),
            name in "[a-zA-Z0-9]{0,16}",
            count in any::<i64>(),
            omit_count in any::<bool>(),
            omit_enabled in any::<bool>(),
        ) {
            let user_events = Arc::new(AtomicUsize::new(0));
            let registry = Registry {
                behavior: Behavior::Value,
                user_events: Arc::clone(&user_events),
                barrier: None,
            };
            let outcomes = Arc::new(Mutex::new(Vec::new()));
            let sink = Sink { outcomes: Arc::clone(&outcomes), fail: false };
            let cancellation = ModuleCancellation::default();
            let (context, _) = context("validation", cancellation);
            let mut arguments = vec![argument("name", json!(name))];
            if !omit_count { arguments.push(argument("count", json!(count))); }
            if !omit_enabled { arguments.push(argument("enabled", json!(false))); }
            if reverse { arguments.reverse(); }
            let mut parent = Some(json!({"state": "ready"}));
            match mode {
                1 => arguments.retain(|argument| argument.name.as_str() != "name"),
                2 => arguments.push(argument("name", json!("duplicate"))),
                3 => arguments.push(argument("unknown", json!(true))),
                4 => parent = Some(json!("bad-parent")),
                5 => {
                    let selected = arguments.iter_mut().find(|argument| argument.name.as_str() == "name").expect("name exists");
                    selected.value = ModuleJson::new(json!(42));
                }
                6 => parent = None,
                _ => {}
            }
            let result = futures::executor::block_on(dispatch(
                &registry,
                envelope("validation", parent, arguments),
                context,
                &sink,
            ));

            if mode == 0 {
                prop_assert!(result.is_ok());
                prop_assert_eq!(user_events.load(Ordering::SeqCst), 1);
                let outcomes = outcomes.lock().expect("outcome lock");
                prop_assert_eq!(outcomes.len(), 1);
                let expected = json!({
                    "count": if omit_count { 7 } else { count },
                    "enabled": omit_enabled,
                    "name": name,
                });
                let preserved = matches!(outcomes.first(), Some(CallOutcome::Value(value)) if value.as_json() == &expected);
                prop_assert!(preserved);
            } else {
                prop_assert!(result.is_err());
                prop_assert_eq!(user_events.load(Ordering::SeqCst), 0);
                prop_assert!(outcomes.lock().expect("outcome lock").is_empty());
            }
        }

        #[test]
        fn property_18_failure_close_precedence_deterministic(
            mode in 0_u8..6,
            close_fails in any::<bool>(),
        ) {
            let behavior = match mode {
                1 => Behavior::Application,
                2 => Behavior::Panic,
                3 => Behavior::EncodeFailure,
                _ => Behavior::Value,
            };
            let user_events = Arc::new(AtomicUsize::new(0));
            let registry = Registry { behavior, user_events, barrier: None };
            let outcomes = Arc::new(Mutex::new(Vec::new()));
            let sink = Sink { outcomes: Arc::clone(&outcomes), fail: mode == 4 };
            let cancellation = ModuleCancellation::default();
            if mode == 5 { cancellation.cancel(); }
            let (context, _) = context("precedence", cancellation);
            let operation = futures::executor::block_on(dispatch(
                &registry,
                envelope("precedence", Some(json!({})), vec![argument("name", json!("value"))]),
                context,
                &sink,
            ));
            let close = if close_fails { Err(CloseError::Interrupted) } else { Ok(()) };
            let result = apply_close_precedence(operation, close);

            match mode {
                0 | 1 if close_fails => prop_assert!(matches!(result, Err(DispatchError::Close(_)))),
                0 | 1 => prop_assert!(result.is_ok()),
                2 => prop_assert!(matches!(result, Err(DispatchError::Panicked(_)))),
                3 => {
                    let retained = matches!(result, Err(DispatchError::Invocation(InvocationError::Encode { .. })));
                    prop_assert!(retained);
                }
                4 => {
                    let retained = matches!(result, Err(DispatchError::Publication { .. }));
                    prop_assert!(retained);
                }
                _ => prop_assert!(matches!(result, Err(DispatchError::Cancelled(_)))),
            }
            let expected_publications = usize::from(matches!(mode, 0..=2));
            prop_assert_eq!(outcomes.lock().expect("outcome lock").len(), expected_publications);
        }
    }

    proptest! {
        #![proptest_config(io_proptest_config())]

        #[test]
        fn property_21_concurrent_calls_remain_isolated(
            names in proptest::collection::vec("[a-zA-Z0-9]{1,16}", 2..7),
        ) {
            let runtime = tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .expect("test runtime builds");
            let count = names.len();
            let user_events = Arc::new(AtomicUsize::new(0));
            let registry = Arc::new(Registry {
                behavior: Behavior::PerCall,
                user_events: Arc::clone(&user_events),
                barrier: Some(Arc::new(tokio::sync::Barrier::new(count))),
            });
            let mut sinks = Vec::new();
            let mut aborts = Vec::new();
            let mut futures = Vec::new();
            for (index, name) in names.iter().enumerate() {
                let call_id = if index == 0 { format!("{name}-panic") } else { format!("{name}-{index}") };
                let fixture_root = format!("/fixture/{index}-{name}");
                let outcomes = Arc::new(Mutex::new(Vec::new()));
                let sink = Arc::new(Sink { outcomes: Arc::clone(&outcomes), fail: false });
                let (context, call_aborts) = context(&call_id, ModuleCancellation::default());
                let envelope = envelope(&call_id, Some(json!({"fixture_root": fixture_root.clone()})), vec![argument("name", json!(name))]);
                let registry = Arc::clone(&registry);
                let call_sink = Arc::clone(&sink);
                futures.push(async move { dispatch(registry.as_ref(), envelope, context, call_sink.as_ref()).await });
                sinks.push((outcomes, json!({"call_id": call_id, "fixture_root": fixture_root, "name": name})));
                aborts.push(call_aborts);
            }
            let results = runtime.block_on(futures::future::join_all(futures));
            runtime.block_on(tokio::task::yield_now());
            prop_assert!(matches!(results.first(), Some(Err(DispatchError::Panicked(_)))));
            prop_assert!(results.iter().skip(1).all(Result::is_ok));
            prop_assert_eq!(user_events.load(Ordering::SeqCst), count);
            for (index, (outcomes, expected)) in sinks.into_iter().enumerate() {
                let outcomes = outcomes.lock().expect("outcome lock");
                prop_assert_eq!(outcomes.len(), 1);
                if index == 0 {
                    let isolated = matches!(outcomes.first(), Some(CallOutcome::Error { kind: ErrorOutcomeKind::Panic, .. }));
                    prop_assert!(isolated);
                } else {
                    let isolated = matches!(outcomes.first(), Some(CallOutcome::Value(value)) if value.as_json() == &expected);
                    prop_assert!(isolated);
                }
            }
            prop_assert!(aborts.iter().all(|aborts| aborts.load(Ordering::SeqCst) == 1));
        }

        #[test]
        fn property_22_cancellation_publication_one_winner_inputs(
            cancel_first in any::<bool>(),
            publication_fails in any::<bool>(),
        ) {
            let election = ResultElection::default();
            prop_assert!(election.begin_publication());
            if cancel_first {
                prop_assert!(election.cancel());
                prop_assert!(!election.accept_publication());
                prop_assert_eq!(election.state(), ResultElectionState::Cancelled);
            } else if publication_fails {
                election.fail_publication();
                prop_assert!(!election.cancel());
                prop_assert_eq!(election.state(), ResultElectionState::PublicationFailed);
            } else {
                prop_assert!(election.accept_publication());
                prop_assert!(!election.cancel());
                prop_assert_eq!(election.state(), ResultElectionState::Published);
            }
        }
    }

    #[test]
    fn property_22_cancellation_publication_one_winner() {
        loom::model(|| {
            use loom::sync::Arc;
            use loom::sync::atomic::{AtomicU8, Ordering};

            let state = Arc::new(AtomicU8::new(ResultElectionState::Publishing as u8));
            let cancel_state = Arc::clone(&state);
            let publish_state = Arc::clone(&state);
            let cancel = loom::thread::spawn(move || {
                cancel_state
                    .compare_exchange(
                        ResultElectionState::Publishing as u8,
                        ResultElectionState::Cancelled as u8,
                        Ordering::AcqRel,
                        Ordering::Acquire,
                    )
                    .is_ok()
            });
            let publish = loom::thread::spawn(move || {
                publish_state
                    .compare_exchange(
                        ResultElectionState::Publishing as u8,
                        ResultElectionState::Published as u8,
                        Ordering::AcqRel,
                        Ordering::Acquire,
                    )
                    .is_ok()
            });
            let cancelled = cancel.join().expect("cancel model joins");
            let published = publish.join().expect("publish model joins");
            assert_ne!(cancelled, published);
            assert!(matches!(
                ResultElectionState::from_raw(state.load(Ordering::Acquire)),
                ResultElectionState::Cancelled | ResultElectionState::Published
            ));
        });
    }
}
