//! Production nested-session adapter for generated module entrypoints.
//!
//! Engine queries stop at this boundary. The adapter reads one complete active call,
//! converts it to the same engine-independent envelope used by direct fixtures, and
//! delegates all branch, validation, dispatch, and result-election semantics to the
//! module runtime. It owns neither authoring grammar nor descriptor construction.

use std::error::Error;
use std::fmt;

use tracing_opentelemetry::OpenTelemetrySpanExt;

use super::context::{CurrentCall, ModuleCancellation, ModuleContextBase};
use super::dispatch::{
    CallOutcome, DispatchError, DispatchRegistry, RegistrationSink, ResultPublishError, ResultSink,
    handle_call,
};
use super::wire::{
    CallEnvelope, CallIdentity, CallSelector, ModuleFunctionName, ModuleJson, ModuleWireName,
    NamedModuleArgument,
};
use crate::{Client, CloseError, ConnectError, FunctionCall, Json, Query, QueryError};

/// Failure while adapting one existing-session engine call to module semantics.
#[doc(hidden)]
#[derive(Debug)]
pub enum ModuleEntrypointError {
    /// The generated binary could not join the engine-provided nested session.
    Connect(ConnectError),
    /// One active-call field or terminal result query failed.
    Query(QueryError),
    /// Engine call data could not be represented by the closed module envelope.
    InvalidCall,
    /// The production dispatcher rejected or failed the adapted call.
    Dispatch(DispatchError),
    /// Explicit close failed after an otherwise successful operation.
    Close(CloseError),
}

impl fmt::Display for ModuleEntrypointError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Connect(_) => "module entrypoint could not join the nested session",
            Self::Query(_) => "module entrypoint query failed",
            Self::InvalidCall => "engine call data is invalid for module dispatch",
            Self::Dispatch(_) => "module call dispatch failed",
            Self::Close(_) => "module entrypoint session close failed",
        })
    }
}

impl Error for ModuleEntrypointError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Connect(error) => Some(error),
            Self::Query(error) => Some(error),
            Self::Dispatch(error) => Some(error),
            Self::Close(error) => Some(error),
            Self::InvalidCall => None,
        }
    }
}

impl From<QueryError> for ModuleEntrypointError {
    fn from(error: QueryError) -> Self {
        Self::Query(error)
    }
}

/// Runs one generated registry against the engine-provided nested session.
///
/// `make_registration_sink` binds descriptor-specific generated registration code to
/// the active query root. The closure is evaluated only after one connection exists,
/// so generated code cannot reconnect or capture process-global client state.
#[doc(hidden)]
pub async fn run_module_entrypoint<R, G, F>(
    registry: R,
    make_registration_sink: F,
) -> Result<(), ModuleEntrypointError>
where
    R: DispatchRegistry,
    G: RegistrationSink,
    F: FnOnce(Query) -> G,
{
    let client = crate::connect()
        .await
        .map_err(ModuleEntrypointError::Connect)?;
    let operation = run_with_client(&client, &registry, make_registration_sink).await;
    let close = client.close().await;

    // Closing is unconditional, but cannot replace a more useful operation failure.
    match operation {
        Err(primary) => Err(primary),
        Ok(()) => close.map_err(ModuleEntrypointError::Close),
    }
}

async fn run_with_client<R, G, F>(
    client: &Client,
    registry: &R,
    make_registration_sink: F,
) -> Result<(), ModuleEntrypointError>
where
    R: DispatchRegistry,
    G: RegistrationSink,
    F: FnOnce(Query) -> G,
{
    let query = client.query();
    let function_call = query.current_function_call();
    let envelope = read_envelope(&function_call, registry).await?;
    let selector = envelope.identity.selector().clone();
    let current_call = CurrentCall::new(envelope.identity.call_id(), selector)
        .map_err(|_| ModuleEntrypointError::InvalidCall)?;
    let context = ModuleContextBase::new(
        client.query_builder(),
        ModuleCancellation::default(),
        tracing::Span::current().context(),
        current_call,
    );
    let registration_sink = make_registration_sink(query.clone());
    let result_sink = EngineResultSink {
        query,
        function_call,
    };

    handle_call(
        registry,
        envelope,
        context,
        &registration_sink,
        &result_sink,
    )
    .await
    .map(|_| ())
    .map_err(ModuleEntrypointError::Dispatch)
}

async fn read_envelope<R: DispatchRegistry>(
    call: &FunctionCall,
    registry: &R,
) -> Result<CallEnvelope, ModuleEntrypointError> {
    let (id, name, parent_name, parent, input_args) = tokio::try_join!(
        call.id(),
        call.name(),
        call.parent_name(),
        call.parent(),
        call.input_args(),
    )?;

    let selector = if parent_name.is_empty() {
        CallSelector::Registration
    } else {
        CallSelector::Invocation {
            parent_wire_name: ModuleWireName::new(parent_name)
                .map_err(|_| ModuleEntrypointError::InvalidCall)?,
            function_wire_name: ModuleFunctionName::new(name)
                .map_err(|_| ModuleEntrypointError::InvalidCall)?,
        }
    };
    let identity = CallIdentity::new(id.into_inner(), selector.clone())
        .map_err(|_| ModuleEntrypointError::InvalidCall)?;
    let parent = match &selector {
        CallSelector::Registration => None,
        CallSelector::Invocation {
            parent_wire_name,
            function_wire_name,
        } => {
            let constructor = registry.descriptor().functions.iter().any(|function| {
                function.parent_wire_name == parent_wire_name.as_str()
                    && function.function_wire_name == function_wire_name.as_str()
                    && function.constructor
            });
            (!constructor).then(|| decode_json(parent)).transpose()?
        }
    };
    let mut arguments = Vec::with_capacity(input_args.len());
    for input in input_args {
        let (name, value) = tokio::try_join!(input.name(), input.value())?;
        arguments.push(NamedModuleArgument {
            name: ModuleWireName::new(name).map_err(|_| ModuleEntrypointError::InvalidCall)?,
            value: decode_json(value)?,
        });
    }

    Ok(CallEnvelope::new(identity, parent, arguments))
}

fn decode_json(value: Json) -> Result<ModuleJson, ModuleEntrypointError> {
    ModuleJson::decode(value.as_str().as_bytes()).map_err(|_| ModuleEntrypointError::InvalidCall)
}

struct EngineResultSink {
    query: Query,
    function_call: FunctionCall,
}

impl ResultSink for EngineResultSink {
    fn publish<'a>(
        &'a self,
        outcome: CallOutcome,
    ) -> super::codec::__private::ModuleBoxFuture<'a, Result<(), ResultPublishError>> {
        Box::pin(async move {
            match outcome {
                CallOutcome::Value(value) => {
                    let encoded = serde_json::to_string(value.as_json())
                        .map_err(ResultPublishError::with_source)?;
                    self.function_call
                        .return_value(Json::new(encoded))
                        .await
                        .map_err(ResultPublishError::with_source)
                }
                CallOutcome::Error { error, .. } => {
                    let mut target_error = self.query.error(error.message());
                    for (name, detail) in error.details() {
                        let encoded = serde_json::to_string(detail.as_json())
                            .map_err(ResultPublishError::with_source)?;
                        target_error = target_error.with_value(name, Json::new(encoded));
                    }
                    self.function_call
                        .return_error(target_error)
                        .await
                        .map_err(ResultPublishError::with_source)
                }
            }
        })
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
    use crate::ClientConfig;
    use crate::connection::{EngineConnection, EngineConnectionError};
    use crate::graphql::{RawRequest, RawResponse, ResponseData};
    use crate::module::codec::__private::ModuleBoxFuture;
    use crate::module::dispatch::{InvocationError, PreparedCall, RegistrationError};
    use crate::module::view::{ArgumentView, FunctionView, ModuleDescriptorView, RegistrationView};
    use crate::test_support::io_proptest_config;

    static ARGUMENTS: &[ArgumentView] = &[ArgumentView {
        wire_name: "value",
        required: true,
        default_json: None,
    }];
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
            function_wire_name: "echo",
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

    #[derive(Clone, Copy)]
    enum InvocationBehavior {
        Value,
        ApplicationError,
        Panic,
    }

    struct Registry {
        invocations: Arc<AtomicUsize>,
        behavior: InvocationBehavior,
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
            _context: ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, InvocationError>> {
            Box::pin(async move {
                self.invocations.fetch_add(1, Ordering::SeqCst);
                match self.behavior {
                    InvocationBehavior::Value => Ok(call
                        .arguments
                        .into_iter()
                        .next()
                        .unwrap_or_else(|| ModuleJson::new(json!({"constructed": true})))),
                    InvocationBehavior::ApplicationError => Err(InvocationError::Application(
                        crate::ModuleError::new("fixture application error"),
                    )),
                    InvocationBehavior::Panic => panic!("contained fixture panic"),
                }
            })
        }
    }

    struct RegistrationRecorder {
        registrations: Arc<AtomicUsize>,
        fail: bool,
    }

    impl RegistrationSink for RegistrationRecorder {
        fn serve<'a>(
            &'a self,
            registration: &'a RegistrationView,
        ) -> ModuleBoxFuture<'a, Result<(), RegistrationError>> {
            Box::pin(async move {
                assert_eq!(registration, &REGISTRATION);
                self.registrations.fetch_add(1, Ordering::SeqCst);
                if self.fail {
                    Err(RegistrationError::new())
                } else {
                    Ok(())
                }
            })
        }
    }

    #[derive(Clone)]
    struct RecordingConnection {
        registration: bool,
        constructor: bool,
        fail_parent: bool,
        malformed_value: bool,
        unknown_function: bool,
        fail_publication: bool,
        requests: Arc<Mutex<Vec<String>>>,
        closes: Arc<AtomicUsize>,
    }

    #[async_trait]
    impl EngineConnection for RecordingConnection {
        async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
            let document = request.query().to_owned();
            self.requests
                .lock()
                .expect("request recorder lock")
                .push(document.clone());
            if self.fail_parent && document == "query{currentFunctionCall{parent}}" {
                return Err(EngineConnectionError::new(
                    crate::EngineConnectionErrorKind::Protocol,
                ));
            }
            if self.fail_publication
                && (document.contains("returnValue") || document.contains("returnError"))
            {
                return Err(EngineConnectionError::new(
                    crate::EngineConnectionErrorKind::Protocol,
                ));
            }
            let data = match document.as_str() {
                "query{currentFunctionCall{id}}" => {
                    json!({"currentFunctionCall": {"id": "call-1"}})
                }
                "query{currentFunctionCall{name}}" => {
                    json!({"currentFunctionCall": {"name": if self.registration { "ignored" } else if self.constructor { "" } else if self.unknown_function { "absent" } else { "echo" }}})
                }
                "query{currentFunctionCall{parentName}}" => {
                    json!({"currentFunctionCall": {"parentName": if self.registration { "" } else { "Fixture" }}})
                }
                "query{currentFunctionCall{parent}}" => {
                    json!({"currentFunctionCall": {"parent": "{\"state\":\"ready\"}"}})
                }
                "query{currentFunctionCall{inputArgs{id}}}" => {
                    json!({"currentFunctionCall": {"inputArgs": if self.registration || self.constructor { json!([]) } else { json!([{"id": "arg-1"}]) }}})
                }
                _ if document.contains("... on FunctionCallArgValue{name}") => {
                    json!({"node": {"name": "value"}})
                }
                _ if document.contains("... on FunctionCallArgValue{value}") => {
                    json!({"node": {"value": if self.malformed_value { "{" } else { "\"hello\"" }}})
                }
                _ if document.starts_with("query{error(") => json!({"error": {"id": "error-1"}}),
                _ if document.contains("returnValue") || document.contains("returnError") => {
                    serde_json::Value::Null
                }
                _ => panic!("unexpected recording query: {document}"),
            };
            Ok(RawResponse::new(if data.is_null() {
                ResponseData::Null
            } else {
                ResponseData::Value(data)
            }))
        }

        async fn close(&self) -> Result<(), EngineConnectionError> {
            self.closes.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }

        fn abort(&self) {}
    }

    #[derive(Clone, Copy)]
    struct HarnessCase {
        registration: bool,
        constructor: bool,
        fail_parent: bool,
        malformed_value: bool,
        unknown_function: bool,
        fail_publication: bool,
        fail_registration: bool,
        behavior: InvocationBehavior,
    }

    impl Default for HarnessCase {
        fn default() -> Self {
            Self {
                registration: false,
                constructor: false,
                fail_parent: false,
                malformed_value: false,
                unknown_function: false,
                fail_publication: false,
                fail_registration: false,
                behavior: InvocationBehavior::Value,
            }
        }
    }

    async fn fixture_client(case: HarnessCase) -> (Client, RecordingConnection) {
        let connection = RecordingConnection {
            registration: case.registration,
            constructor: case.constructor,
            fail_parent: case.fail_parent,
            malformed_value: case.malformed_value,
            unknown_function: case.unknown_function,
            fail_publication: case.fail_publication,
            requests: Arc::new(Mutex::new(Vec::new())),
            closes: Arc::new(AtomicUsize::new(0)),
        };
        let config = ClientConfig::builder()
            .connection(Box::new(connection.clone()))
            .build()
            .expect("explicit fixture client config must validate");
        let client = crate::connect_with(config)
            .await
            .expect("explicit fixture client must connect");
        (client, connection)
    }

    #[tokio::test]
    async fn production_adapter_reads_complete_call_and_publishes_on_the_same_session() {
        let case = HarnessCase::default();
        let (client, connection) = fixture_client(case).await;
        let invocations = Arc::new(AtomicUsize::new(0));
        let registrations = Arc::new(AtomicUsize::new(0));
        let result = run_with_client(
            &client,
            &Registry {
                invocations: Arc::clone(&invocations),
                behavior: case.behavior,
            },
            |_| RegistrationRecorder {
                registrations: Arc::clone(&registrations),
                fail: case.fail_registration,
            },
        )
        .await;
        assert!(
            result.is_ok(),
            "recorded invocation failed: {result:?}; requests: {:?}",
            connection.requests.lock().expect("request recorder lock")
        );
        client.close().await.expect("fixture session must close");

        let requests = connection.requests.lock().expect("request recorder lock");
        for required in [
            "{id}",
            "{name}",
            "{parentName}",
            "{parent}",
            "{inputArgs{id}}",
        ] {
            assert!(
                requests.iter().any(|request| request.contains(required)),
                "active call did not read {required}"
            );
        }
        assert!(
            requests
                .iter()
                .any(|request| request.contains("returnValue"))
        );
        assert_eq!(invocations.load(Ordering::SeqCst), 1);
        assert_eq!(registrations.load(Ordering::SeqCst), 0);
        assert_eq!(connection.closes.load(Ordering::SeqCst), 1);
    }

    proptest! {
        #![proptest_config(io_proptest_config())]

        #[test]
        fn property_26_direct_harness_exercises_production_semantics(
            scenario in 0_u8..10,
        ) {
            let runtime = tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .expect("direct harness runtime must build");
            runtime.block_on(async {
                let mut case = HarnessCase::default();
                match scenario {
                    0 => case.registration = true,
                    1 => case.fail_parent = true,
                    2 => case.malformed_value = true,
                    3 => case.unknown_function = true,
                    4 => case.fail_publication = true,
                    5 => {
                        case.registration = true;
                        case.fail_registration = true;
                    }
                    6 => case.behavior = InvocationBehavior::ApplicationError,
                    7 => case.behavior = InvocationBehavior::Panic,
                    8 => case.constructor = true,
                    _ => {}
                }
                let (client, connection) = fixture_client(case).await;
                let invocations = Arc::new(AtomicUsize::new(0));
                let registrations = Arc::new(AtomicUsize::new(0));
                let result = run_with_client(
                    &client,
                    &Registry {
                        invocations: Arc::clone(&invocations),
                        behavior: case.behavior,
                    },
                    |_| RegistrationRecorder {
                        registrations: Arc::clone(&registrations),
                        fail: case.fail_registration,
                    },
                ).await;
                client.close().await.expect("direct harness session must close");

                if case.fail_parent {
                    prop_assert!(matches!(result, Err(ModuleEntrypointError::Query(_))));
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 0);
                    prop_assert_eq!(registrations.load(Ordering::SeqCst), 0);
                } else if case.malformed_value {
                    prop_assert!(matches!(result, Err(ModuleEntrypointError::InvalidCall)));
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 0);
                    prop_assert_eq!(registrations.load(Ordering::SeqCst), 0);
                } else if case.unknown_function {
                    prop_assert!(matches!(result, Err(ModuleEntrypointError::Dispatch(DispatchError::Preparation(_)))));
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 0);
                } else if case.fail_registration {
                    prop_assert!(matches!(result, Err(ModuleEntrypointError::Dispatch(DispatchError::Registration(_)))));
                    prop_assert_eq!(registrations.load(Ordering::SeqCst), 1);
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 0);
                } else if case.registration {
                    prop_assert!(result.is_ok());
                    prop_assert_eq!(registrations.load(Ordering::SeqCst), 1);
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 0);
                } else if case.fail_publication {
                    let publication_failed = matches!(
                        result,
                        Err(ModuleEntrypointError::Dispatch(
                            DispatchError::Publication { .. }
                        ))
                    );
                    prop_assert!(publication_failed);
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 1);
                } else if matches!(case.behavior, InvocationBehavior::Panic) {
                    prop_assert!(matches!(result, Err(ModuleEntrypointError::Dispatch(DispatchError::Panicked(_)))));
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 1);
                } else {
                    prop_assert!(result.is_ok());
                    prop_assert_eq!(registrations.load(Ordering::SeqCst), 0);
                    prop_assert_eq!(invocations.load(Ordering::SeqCst), 1);
                }
                prop_assert_eq!(connection.closes.load(Ordering::SeqCst), 1);
                Ok(())
            })?;
        }
    }
}
