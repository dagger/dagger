//! Engine-free generated-client request, re-entry, and lifecycle properties.

#![cfg(feature = "gen")]

#[allow(dead_code, unused_imports)]
#[path = "fixtures/generated_client/mod.rs"]
mod dagger_client;

use std::collections::VecDeque;
use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use dagger_client::minimal::SearchOpts;
use dagger_client::prelude::*;
use dagger_sdk::{
    ClientConfig, EngineConnection, EngineConnectionError, Id, IdInput, IntoID, QueryBuildError,
    QueryBuildErrorKind, QueryError, RawRequest, RawResponse, ResponseData,
};
use serde_json::{Value, json};

#[derive(Clone, Default)]
struct RecordingConnection {
    requests: Arc<Mutex<Vec<String>>>,
    responses: Arc<Mutex<VecDeque<RawResponse>>>,
    execute_calls: Arc<AtomicUsize>,
    close_calls: Arc<AtomicUsize>,
}

impl RecordingConnection {
    fn respond_with(&self, value: Value) {
        self.responses
            .lock()
            .expect("response queue remains available")
            .push_back(RawResponse::new(ResponseData::Value(value)));
    }

    fn requests(&self) -> Vec<String> {
        self.requests
            .lock()
            .expect("request log remains available")
            .clone()
    }

    fn execute_calls(&self) -> usize {
        self.execute_calls.load(Ordering::SeqCst)
    }
}

#[async_trait]
impl EngineConnection for RecordingConnection {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.execute_calls.fetch_add(1, Ordering::SeqCst);
        self.requests
            .lock()
            .expect("request log remains available")
            .push(request.query().to_owned());
        Ok(self
            .responses
            .lock()
            .expect("response queue remains available")
            .pop_front()
            .expect("each admitted fixture request has one response"))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        self.close_calls.fetch_add(1, Ordering::SeqCst);
        Ok(())
    }

    fn abort(&self) {}
}

async fn configured_client(connection: RecordingConnection) -> dagger_sdk::Client {
    let config = ClientConfig::builder()
        .connection(Box::new(connection))
        .graphql_execution_timeout(Duration::from_secs(1))
        .build()
        .expect("recording configuration is valid");
    dagger_sdk::connect_with(config)
        .await
        .expect("an injected connection requires no engine")
}

#[tokio::test]
async fn property_07_module_root_composition_preserves_shared_client() {
    // Invariant: every generated path retains one session and performs no construction I/O.
    for schedule in 0_u8..128 {
        let connection = RecordingConnection::default();
        let client = configured_client(connection.clone()).await;
        let close_handle = client.clone();
        let root = if schedule & 1 == 0 {
            client.minimal()
        } else {
            client.query_builder().minimal()
        };
        let root = if schedule & 2 == 0 {
            root
        } else {
            root.clone()
        };
        let helper = root.helper();
        let container = root.container();
        assert_eq!(connection.execute_calls(), 0);

        if schedule & 4 != 0 {
            drop(client);
        }

        if schedule & 32 != 0 {
            close_handle.close().await.expect("shared session closes");
            assert!(matches!(
                root.message().await,
                Err(QueryError::Request(dagger_sdk::RequestError::ClientClosed))
            ));
            assert_eq!(connection.execute_calls(), 0);
            assert_eq!(connection.close_calls.load(Ordering::SeqCst), 1);
            continue;
        }

        match (schedule >> 3) % 3 {
            0 => {
                connection.respond_with(json!({"minimal": {"message": "ready"}}));
                assert_eq!(
                    root.message().await.expect("module scalar executes"),
                    "ready"
                );
                assert_eq!(
                    connection.requests().last().map(String::as_str),
                    Some("query{minimal{message}}")
                );
            }
            1 => {
                connection.respond_with(json!({"minimal": {"helper": {"id": "helper-1"}}}));
                assert_eq!(
                    helper.id().await.expect("local handle executes").as_str(),
                    "helper-1"
                );
                assert_eq!(
                    connection.requests().last().map(String::as_str),
                    Some("query{minimal{helper{id}}}")
                );
            }
            _ => {
                connection.respond_with(json!({"minimal": {"container": {"id": "ctr-1"}}}));
                assert_eq!(
                    container.id().await.expect("Core handle executes").as_str(),
                    "ctr-1"
                );
                assert_eq!(
                    connection.requests().last().map(String::as_str),
                    Some("query{minimal{container{id}}}")
                );
            }
        }

        close_handle.close().await.expect("shared session closes");
        assert_eq!(connection.close_calls.load(Ordering::SeqCst), 1);
    }
}

#[tokio::test]
async fn property_09_wrappers_omission_wire_names_id_reentry_faithful() {
    // Invariant: omission, values, wrappers, and re-entry preserve their exact wire model.
    let connection = RecordingConnection::default();
    let client = configured_client(connection.clone()).await;
    let root = client.minimal();

    for case in 0_u16..256 {
        let mut opts = SearchOpts::default();
        let mut expected = Vec::<String>::new();
        match case % 4 {
            1 => {
                opts = opts.with_count(0);
                expected.push("count:0".to_owned());
            }
            2 => {
                opts = opts.with_count(i64::from(case));
                expected.push(format!("count:{case}"));
            }
            3 => {
                opts = opts.with_count(-1);
                expected.push("count:-1".to_owned());
            }
            _ => {}
        }
        match (case / 4) % 4 {
            1 => {
                opts = opts.with_enabled(false);
                expected.push("enabled:false".to_owned());
            }
            2 => {
                opts = opts.with_enabled(true);
                expected.push("enabled:true".to_owned());
            }
            3 => {
                opts = opts.with_enabled_null();
                expected.push("enabled:null".to_owned());
            }
            _ => {}
        }
        match (case / 16) % 3 {
            1 => {
                opts = opts.with_label(String::new());
                expected.push("label:\"\"".to_owned());
            }
            2 => {
                opts = opts.with_label("wire".to_owned());
                expected.push("label:\"wire\"".to_owned());
            }
            _ => {}
        }
        connection.respond_with(json!({"minimal": {"search": "matched"}}));
        assert_eq!(
            root.search_opts(opts)
                .await
                .expect("option schedule executes"),
            "matched"
        );
        let arguments = if expected.is_empty() {
            String::new()
        } else {
            format!("({})", expected.join(", "))
        };
        assert_eq!(
            connection.requests().last().expect("request was recorded"),
            &format!("query{{minimal{{search{arguments}}}}}")
        );
    }

    for case in 0_u8..128 {
        if case & 1 == 0 {
            let first = format!("first-{case}");
            let third = format!("third-{case}");
            let ids = if case & 2 == 0 {
                vec![Some(first.as_str()), None, Some(third.as_str())]
            } else {
                vec![None]
            };
            connection.respond_with(json!({"minimal": {"items": ids
                .iter()
                .map(|id| id.map(|id| json!({"id": id})).unwrap_or(Value::Null))
                .collect::<Vec<_>>()}}));
            let items = root.items().await.expect("identifier shape decodes");
            assert_eq!(items.len(), ids.len());
            assert_eq!(
                connection.requests().last().map(String::as_str),
                Some("query{minimal{items{id}}}")
            );
            if let Some(item) = items.into_iter().flatten().next() {
                let expected = ids.into_iter().flatten().next().expect("an ID exists");
                connection.respond_with(json!({"node": {"id": expected}}));
                assert_eq!(
                    item.id()
                        .await
                        .expect("re-entered handle executes")
                        .as_str(),
                    expected
                );
                let expected_query =
                    format!("query{{node(id:\"{expected}\"){{... on MinimalItem{{id}}}}}}");
                assert_eq!(
                    connection.requests().last().map(String::as_str),
                    Some(expected_query.as_str())
                );
            }
        } else {
            let lazy_id = format!("lazy-item-{case}");
            connection.respond_with(json!({"minimal": {"item": {"id": lazy_id.as_str()}}}));
            let item = root
                .item()
                .await
                .expect("nullable identifier decodes")
                .expect("fixture returns one item");
            connection.respond_with(json!({"node": {"id": lazy_id.as_str()}}));
            connection.respond_with(json!({"minimal": {"useItem": "used"}}));
            assert_eq!(root.use_item(item).await.expect("lazy ID resolves"), "used");
            let requests = connection.requests();
            let expected_lookup =
                format!("query{{node(id:\"{lazy_id}\"){{... on MinimalItem{{id}}}}}}");
            let expected_use = format!("query{{minimal{{useItem(item:\"{lazy_id}\")}}}}");
            assert_eq!(
                &requests[requests.len() - 2..],
                [expected_lookup, expected_use]
            );
        }
    }

    client.close().await.expect("fixture session closes");
}

#[derive(Clone)]
struct FailingIdentifier;

impl IntoID<Id> for FailingIdentifier {
    fn into_id(self) -> Pin<Box<dyn core::future::Future<Output = Result<Id, QueryError>> + Send>> {
        Box::pin(async {
            Err(QueryError::Build(QueryBuildError::new(
                QueryBuildErrorKind::LazyIdentifier,
            )))
        })
    }
}

#[tokio::test]
async fn recursive_identifier_failure_rejects_the_containing_request() {
    let connection = RecordingConnection::default();
    let client = configured_client(connection.clone()).await;
    let root = client.minimal();
    let values = vec![
        Some(IdInput::new(Id::new("ready"))),
        Some(IdInput::generated_lazy(FailingIdentifier)),
    ];

    let error = root
        .use_items(values)
        .await
        .expect_err("the second identifier fails");
    assert!(matches!(error, QueryError::Build(_)));
    assert_eq!(connection.execute_calls(), 0);
    assert!(connection.requests().is_empty());
    assert!(!format!("{error:?}").contains("ready"));

    client.close().await.expect("fixture session closes");
}

#[tokio::test]
async fn exact_version_bridge_covers_direct_ids_core_reentry_and_explicit_null() {
    let connection = RecordingConnection::default();
    let client = configured_client(connection.clone()).await;
    let document = client
        .query_builder()
        .select("lookup")
        .generated_argument_id("id", Id::new("ctr-direct"))
        .document()
        .await
        .expect("ready identifier builds without I/O");
    assert_eq!(document, "query{lookup(id:\"ctr-direct\")}");
    assert_eq!(connection.execute_calls(), 0);

    connection.respond_with(json!({"minimal": {"maybeContainer": {"id": "ctr-reentry"}}}));
    let container = client
        .minimal()
        .maybe_container()
        .await
        .expect("Core ID probe succeeds")
        .expect("fixture returns a Core handle");
    connection.respond_with(json!({"node": {"id": "ctr-reentry"}}));
    assert_eq!(
        container
            .id()
            .await
            .expect("Core re-entry executes")
            .as_str(),
        "ctr-reentry"
    );

    connection.respond_with(json!({"minimal": {"search": "null"}}));
    assert_eq!(
        client
            .minimal()
            .search_opts(SearchOpts::default().with_item_null())
            .await
            .expect("explicit null ID executes"),
        "null"
    );
    assert_eq!(
        connection.requests().last().map(String::as_str),
        Some("query{minimal{search(item:null)}}")
    );

    connection.respond_with(json!({"minimal": {"sync": "minimal-reentry"}}));
    let synced = client
        .minimal()
        .sync()
        .await
        .expect("expected-type self return resolves");
    connection.respond_with(json!({"node": {"message": "re-entered"}}));
    assert_eq!(
        synced.message().await.expect("re-entered root executes"),
        "re-entered"
    );
    assert_eq!(
        connection.requests().last().map(String::as_str),
        Some("query{node(id:\"minimal-reentry\"){... on Minimal{message}}}")
    );

    client.close().await.expect("fixture session closes");
}
