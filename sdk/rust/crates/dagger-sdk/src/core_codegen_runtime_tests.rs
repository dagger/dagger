//! Runtime properties for scalar values, typed IDs, wrapper projection, and re-entry.

use std::collections::VecDeque;
use std::future::Future;
use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use proptest::prelude::*;
use serde_json::{Map, Value, json};

use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
use crate::errors::{QueryBuildErrorKind, QueryError, RequestError};
use crate::graphql::{GraphQlError, RawRequest, RawResponse, ResponseData};
use crate::lifecycle::SessionHandle;
use crate::loadable::private::Sealed;
use crate::query::{QueryBuilder, Selection, query, reenter};
use crate::test_support::{io_proptest_config, proptest_config};
use crate::{Id, IdInput, IntoID, Json, Platform};

#[derive(Clone)]
struct TestHandle {
    session: SessionHandle,
    selection: Selection,
}

impl TestHandle {
    fn select(&self, field: &str) -> Self {
        Self {
            session: self.session.clone(),
            selection: self.selection.select(field),
        }
    }
}

impl Sealed for TestHandle {
    fn graphql_type() -> &'static str {
        "TestObject"
    }

    fn from_query(session: SessionHandle, selection: Selection) -> Self {
        Self { session, selection }
    }
}

impl IntoID<Id> for TestHandle {
    fn into_id(self) -> Pin<Box<dyn Future<Output = Result<Id, QueryError>> + Send>> {
        Box::pin(async move { self.selection.select("id").execute(&self.session).await })
    }
}

#[derive(Clone)]
struct RuntimeProbe {
    state: Arc<ProbeState>,
}

struct ProbeState {
    outcomes: Mutex<VecDeque<ProbeOutcome>>,
    requests: Mutex<Vec<String>>,
    aborts: AtomicUsize,
    execute_started: tokio::sync::Notify,
}

enum ProbeOutcome {
    Response(RawResponse),
    Error(EngineConnectionErrorKind),
    Pending,
}

impl RuntimeProbe {
    fn new(outcomes: impl IntoIterator<Item = ProbeOutcome>) -> Self {
        Self {
            state: Arc::new(ProbeState {
                outcomes: Mutex::new(outcomes.into_iter().collect()),
                requests: Mutex::new(Vec::new()),
                aborts: AtomicUsize::new(0),
                execute_started: tokio::sync::Notify::new(),
            }),
        }
    }

    fn request_count(&self) -> usize {
        self.state.requests.lock().expect("request lock").len()
    }

    fn requests(&self) -> Vec<String> {
        self.state.requests.lock().expect("request lock").clone()
    }

    async fn wait_for_execute(&self) {
        self.state.execute_started.notified().await;
    }
}

#[async_trait]
impl EngineConnection for RuntimeProbe {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.state
            .requests
            .lock()
            .expect("request lock")
            .push(request.query().to_owned());
        self.state.execute_started.notify_one();
        let outcome = self
            .state
            .outcomes
            .lock()
            .expect("outcome lock")
            .pop_front()
            .unwrap_or_else(|| ProbeOutcome::Error(EngineConnectionErrorKind::Protocol));
        match outcome {
            ProbeOutcome::Response(response) => Ok(response),
            ProbeOutcome::Error(kind) => Err(EngineConnectionError::new(kind)),
            ProbeOutcome::Pending => std::future::pending().await,
        }
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {
        self.state.aborts.fetch_add(1, Ordering::SeqCst);
    }
}

fn session(probe: RuntimeProbe, timeout: Option<Duration>) -> SessionHandle {
    SessionHandle::new(Box::new(probe), None, timeout)
}

#[derive(Clone)]
struct OrderedIdentifier {
    index: usize,
    value: Id,
    fail_at: Option<usize>,
    evidence: Arc<Mutex<Vec<usize>>>,
}

impl IntoID<Id> for OrderedIdentifier {
    fn into_id(self) -> Pin<Box<dyn Future<Output = Result<Id, QueryError>> + Send>> {
        Box::pin(async move {
            self.evidence
                .lock()
                .expect("evidence lock")
                .push(self.index);
            if self.fail_at == Some(self.index) {
                return Err(QueryError::Request(RequestError::Connection(
                    EngineConnectionError::new(EngineConnectionErrorKind::Unavailable),
                )));
            }
            Ok(self.value)
        })
    }
}

#[test]
fn handwritten_scalars_preserve_exact_wire_values_and_secret_safe_debug() {
    let id = Id::new("opaque\"identifier");
    let json_scalar = Json::new(r#"{"order":[2,1]}"#);
    let platform = Platform::new("linux/arm64/v8");

    assert_eq!(
        serde_json::to_value(&id).expect("serialize ID"),
        json!("opaque\"identifier")
    );
    assert_eq!(
        serde_json::from_value::<Id>(json!("id"))
            .expect("decode ID")
            .as_str(),
        "id"
    );
    assert_eq!(json_scalar.as_str(), r#"{"order":[2,1]}"#);
    assert_eq!(platform.to_string(), "linux/arm64/v8");

    let input = IdInput::<TestHandle>::from(Id::new("must-not-appear"));
    let debug = format!("{input:?}");
    assert!(debug.contains("ready"));
    assert!(!debug.contains("must-not-appear"));
}

#[test]
fn void_accepts_only_its_null_wire_representation() {
    let selection = query().select("result");
    let successful = selection.decode::<()>(RawResponse::new(ResponseData::Value(json!({
        "result": null
    }))));
    assert!(successful.is_ok());

    let represented = selection.decode::<()>(RawResponse::new(ResponseData::Value(json!({
        "result": false
    }))));
    assert!(matches!(represented, Err(QueryError::Decode(_))));
}

#[test]
fn recursive_projection_preserves_each_list_and_nullable_wrapper() {
    let selection = query().select("groups").select("members").select("id");
    let decoded: Vec<Option<Vec<Option<Id>>>> = selection
        .unpack_value(json!({
            "groups": [
                {"members": [{"id": "a"}, null, {"id": "b"}]},
                null,
                {"members": []}
            ]
        }))
        .expect("wrapper-correct projection");
    assert_eq!(
        decoded,
        vec![
            Some(vec![Some(Id::new("a")), None, Some(Id::new("b"))]),
            None,
            Some(Vec::new()),
        ]
    );
}

proptest! {
    #![proptest_config(proptest_config())]

    // Feature: rust-sdk-core-codegen, Property 9: Lazy handles preserve the originating lease
    #[test]
    fn property_09_lazy_handles_preserve_originating_lease(
        fields in proptest::collection::vec("[a-z]{1,8}", 0..8),
        identifier in "[a-zA-Z0-9_-]{1,24}",
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let probe = RuntimeProbe::new([]);
            let session = session(probe.clone(), None);
            let mut handle = TestHandle::from_query(session.clone(), query().select("root"));
            for field in &fields {
                handle = handle.select(field);
            }
            let reentered: TestHandle = reenter(&session, Id::new(identifier.clone()), "TestObject");

            prop_assert_eq!(handle.session.identity(), session.identity());
            prop_assert_eq!(reentered.session.identity(), session.identity());
            prop_assert_eq!(probe.request_count(), 0);
            prop_assert_eq!(
                reentered.selection.build().await.expect("valid re-entry"),
                format!("query{{node(id:\"{identifier}\"){{... on TestObject}}}}")
            );
            Ok(())
        })?;
    }

    // Feature: rust-sdk-core-codegen, Property 10: Nullable handles reflect target presence
    #[test]
    fn property_10_nullable_handles_reflect_target_presence(
        identifier in proptest::option::of("[a-zA-Z0-9_-]{1,24}"),
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let wire = match &identifier {
                Some(value) => json!({"candidate": {"id": value}}),
                None => json!({"candidate": null}),
            };
            let probe = RuntimeProbe::new([ProbeOutcome::Response(RawResponse::new(
                ResponseData::Value(wire),
            ))]);
            let session = session(probe.clone(), None);
            let selection = query().select("candidate").select("id");
            let handle = selection
                .execute_reentry::<TestHandle, Option<Id>>(&session, "TestObject")
                .await
                .expect("valid nullable probe");

            prop_assert_eq!(probe.request_count(), 1);
            prop_assert_eq!(handle.is_some(), identifier.is_some());
            if let Some(handle) = handle {
                prop_assert_eq!(handle.session.identity(), session.identity());
                let document = handle.selection.build().await.expect("valid re-entry");
                prop_assert!(document.contains("... on TestObject"));
            }
            Ok(())
        })?;
    }

    // Feature: rust-sdk-core-codegen, Property 11: Object-list re-entry preserves structure
    #[test]
    fn property_11_object_list_reentry_preserves_structure(
        identifiers in proptest::collection::vec(
            proptest::option::of("[a-zA-Z0-9_-]{1,18}"),
            0..20,
        ),
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let wire = identifiers.iter().map(|identifier| match identifier {
                Some(value) => json!({"id": value}),
                None => Value::Null,
            }).collect::<Vec<_>>();
            let probe = RuntimeProbe::new([ProbeOutcome::Response(RawResponse::new(
                ResponseData::Value(json!({"items": wire})),
            ))]);
            let session = session(probe.clone(), None);
            let handles = query()
                .select("items")
                .select("id")
                .execute_reentry::<TestHandle, Vec<Option<Id>>>(&session, "TestObject")
                .await
                .expect("valid ordered re-entry");

            prop_assert_eq!(handles.len(), identifiers.len());
            for (handle, identifier) in handles.into_iter().zip(identifiers) {
                prop_assert_eq!(handle.is_some(), identifier.is_some());
                if let (Some(handle), Some(identifier)) = (handle, identifier) {
                    prop_assert_eq!(handle.session.identity(), session.identity());
                    prop_assert_eq!(
                        handle.selection.build().await.expect("valid re-entry"),
                        format!("query{{node(id:\"{identifier}\"){{... on TestObject}}}}")
                    );
                }
            }
            prop_assert_eq!(probe.request_count(), 1);
            Ok(())
        })?;
    }

    // Feature: rust-sdk-core-codegen, Property 15: Typed ID compatibility is closed and all-or-nothing
    #[test]
    fn property_15_typed_id_compatibility_closed_all_or_nothing(
        identifiers in proptest::collection::vec("[a-zA-Z0-9_-]{1,18}", 0..16),
        failure_seed in proptest::option::of(any::<usize>()),
        use_ready_ids in any::<bool>(),
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let fail_at = failure_seed
                .filter(|_| !identifiers.is_empty() && !use_ready_ids)
                .map(|seed| seed % identifiers.len());
            let evidence = Arc::new(Mutex::new(Vec::new()));
            let inputs = identifiers
                .iter()
                .enumerate()
                .map(|(index, value)| {
                    if use_ready_ids {
                        IdInput::<TestHandle>::from(Id::new(value))
                    } else {
                        IdInput::<TestHandle>::lazy(OrderedIdentifier {
                            index,
                            value: Id::new(value),
                            fail_at,
                            evidence: Arc::clone(&evidence),
                        })
                    }
                })
                .collect::<Vec<_>>();
            let probe = RuntimeProbe::new([ProbeOutcome::Response(RawResponse::new(
                ResponseData::Value(json!({"consume": true})),
            ))]);
            let session = session(probe.clone(), None);
            let result = query()
                .select("consume")
                .arg_id_input("ids", inputs)
                .execute::<bool>(&session)
                .await;

            let observed = evidence.lock().expect("evidence lock").clone();
            if use_ready_ids {
                prop_assert!(observed.is_empty());
                prop_assert_eq!(probe.request_count(), 1);
                prop_assert_eq!(result.expect("ready IDs execute"), true);
            } else if let Some(index) = fail_at {
                prop_assert_eq!(observed, (0..=index).collect::<Vec<_>>());
                prop_assert_eq!(probe.request_count(), 0);
                let QueryError::Build(error) = result.expect_err("resolver failure") else {
                    return Err(TestCaseError::fail("expected typed build failure"));
                };
                prop_assert_eq!(error.kind(), QueryBuildErrorKind::LazyIdentifier);
                let source = std::error::Error::source(&error).expect("indexed source");
                prop_assert!(source.to_string().contains(&index.to_string()));
            } else {
                prop_assert_eq!(observed, (0..identifiers.len()).collect::<Vec<_>>());
                prop_assert_eq!(probe.request_count(), 1);
                prop_assert_eq!(result.expect("all identifiers resolve"), true);
            }
            Ok(())
        })?;
    }

    // Feature: rust-sdk-core-codegen, Property 16: Expected-type self return is type- and selection-safe
    #[test]
    fn property_16_expected_type_self_return_type_selection_safe(
        identifier in "[a-zA-Z0-9_-]{1,24}",
        suffix in proptest::collection::vec("[a-z]{1,8}", 0..6),
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let probe = RuntimeProbe::new([]);
            let session = session(probe.clone(), None);
            let mut parent: TestHandle = reenter(
                &session,
                Id::new(identifier.clone()),
                TestHandle::graphql_type(),
            );
            for field in suffix {
                parent = parent.select(&field);
            }
            prop_assert_eq!(parent.session.identity(), session.identity());
            let document = parent.selection.build().await.expect("valid self-return");
            let expected_prefix = format!(
                "query{{node(id:\"{}\"){{... on TestObject",
                identifier,
            );
            prop_assert!(document.starts_with(&expected_prefix));
            prop_assert_eq!(probe.request_count(), 0);
            Ok(())
        })?;
    }
}

#[derive(Clone, Copy)]
enum RuntimeScenario {
    Success,
    Closed,
    Timeout,
    Transport,
    GraphQl,
    Exec,
    Decode,
    Cancelled,
}

fn scenario(index: u8) -> RuntimeScenario {
    match index % 8 {
        0 => RuntimeScenario::Success,
        1 => RuntimeScenario::Closed,
        2 => RuntimeScenario::Timeout,
        3 => RuntimeScenario::Transport,
        4 => RuntimeScenario::GraphQl,
        5 => RuntimeScenario::Exec,
        6 => RuntimeScenario::Decode,
        _ => RuntimeScenario::Cancelled,
    }
}

fn scenario_probe(scenario: RuntimeScenario) -> (RuntimeProbe, Option<Duration>) {
    let response = match scenario {
        RuntimeScenario::Success => {
            ProbeOutcome::Response(RawResponse::new(ResponseData::Value(json!({"value": 7}))))
        }
        RuntimeScenario::Timeout => ProbeOutcome::Pending,
        RuntimeScenario::Transport => ProbeOutcome::Error(EngineConnectionErrorKind::Transport),
        RuntimeScenario::GraphQl => ProbeOutcome::Response(
            RawResponse::new(ResponseData::Value(json!({"value": 7})))
                .with_errors(vec![GraphQlError::new("failed")]),
        ),
        RuntimeScenario::Exec => {
            let extensions = Map::from_iter([
                ("_type".to_owned(), json!("EXEC_ERROR")),
                ("exitCode".to_owned(), json!(17)),
                ("cmd".to_owned(), json!(["false"])),
            ]);
            ProbeOutcome::Response(
                RawResponse::new(ResponseData::Value(json!({"value": null}))).with_errors(vec![
                    GraphQlError::new("process failed").with_extensions(extensions),
                ]),
            )
        }
        RuntimeScenario::Decode => ProbeOutcome::Response(RawResponse::new(ResponseData::Value(
            json!({"value": "seven"}),
        ))),
        RuntimeScenario::Closed => {
            ProbeOutcome::Response(RawResponse::new(ResponseData::Value(json!({"value": 7}))))
        }
        RuntimeScenario::Cancelled => ProbeOutcome::Pending,
    };
    let timeout = matches!(scenario, RuntimeScenario::Timeout).then_some(Duration::from_millis(1));
    let outcomes = if matches!(scenario, RuntimeScenario::Cancelled) {
        vec![
            response,
            ProbeOutcome::Response(RawResponse::new(ResponseData::Value(json!({
                "value": 7
            })))),
        ]
    } else {
        vec![response]
    };
    (RuntimeProbe::new(outcomes), timeout)
}

fn result_coordinate(result: Result<i64, QueryError>) -> String {
    match result {
        Ok(value) => format!("ok:{value}"),
        Err(QueryError::Build(error)) => format!("build:{:?}", error.kind()),
        Err(QueryError::Request(RequestError::ClientClosed)) => "request:closed".to_owned(),
        Err(QueryError::Request(RequestError::ExecutionTimeout { .. })) => {
            "request:timeout".to_owned()
        }
        Err(QueryError::Request(RequestError::Connection(error))) => {
            format!("request:connection:{:?}", error.kind())
        }
        Err(QueryError::Request(error)) => format!("request:{error:?}"),
        Err(QueryError::GraphQl { .. }) => "graphql".to_owned(),
        Err(QueryError::Exec { error, .. }) => format!("exec:{:?}", error.exit_code()),
        Err(QueryError::Decode(_)) => "decode".to_owned(),
    }
}

proptest! {
    #![proptest_config(io_proptest_config())]

    // Feature: rust-sdk-core-codegen, Property 12: Executing fields preserve runtime behaviour
    #[test]
    fn property_12_executing_fields_preserve_runtime_behaviour(case in any::<u8>()) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let scenario = scenario(case);
            let (generated_probe, generated_timeout) = scenario_probe(scenario);
            let (reference_probe, reference_timeout) = scenario_probe(scenario);
            let generated_session = session(generated_probe.clone(), generated_timeout);
            let reference_session = session(reference_probe.clone(), reference_timeout);

            if matches!(scenario, RuntimeScenario::Closed) {
                generated_session.close().await.expect("close generated session");
                reference_session.close().await.expect("close reference session");
            }

            if matches!(scenario, RuntimeScenario::Cancelled) {
                let generated_cancelled = tokio::spawn({
                    let session = generated_session.clone();
                    async move { query().select("value").execute::<i64>(&session).await }
                });
                generated_probe.wait_for_execute().await;
                generated_cancelled.abort();
                let _ = generated_cancelled.await;

                let reference_cancelled = tokio::spawn({
                    let builder = QueryBuilder::new(reference_session.clone()).select("value");
                    async move { builder.execute::<i64>().await }
                });
                reference_probe.wait_for_execute().await;
                reference_cancelled.abort();
                let _ = reference_cancelled.await;
            }

            let generated = query().select("value").execute::<i64>(&generated_session).await;
            let reference = QueryBuilder::new(reference_session)
                .select("value")
                .execute::<i64>()
                .await;

            prop_assert_eq!(result_coordinate(generated), result_coordinate(reference));
            prop_assert_eq!(generated_probe.requests(), reference_probe.requests());
            Ok(())
        })?;
    }
}

#[tokio::test]
async fn invalid_nullable_identifier_response_returns_no_partial_handle() {
    let probe = RuntimeProbe::new([ProbeOutcome::Response(RawResponse::new(
        ResponseData::Value(json!({"candidate": {"id": 42}})),
    ))]);
    let session = session(probe.clone(), None);
    let result = query()
        .select("candidate")
        .select("id")
        .execute_reentry::<TestHandle, Option<Id>>(&session, "TestObject")
        .await;
    assert!(matches!(result, Err(QueryError::Decode(_))));
    assert_eq!(probe.request_count(), 1);
}
