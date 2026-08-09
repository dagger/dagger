//! End-to-end checks which consume only the stable public SDK facade.

#![cfg(feature = "gen")]

use std::process::Command;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use async_trait::async_trait;
use dagger_sdk::{
    ClientConfig, EngineConnection, EngineConnectionError, GraphQlError, GraphQlPathSegment,
    RawRequest, RawResponse, RequestError, ResponseData,
};
use serde_json::json;

const DEFAULT_CONNECT_CHILD: &str = "DAGGER_RUST_DEFAULT_CONNECT_CHILD";

#[derive(Clone, Default)]
struct PublicProbe {
    execute_calls: Arc<AtomicUsize>,
    close_calls: Arc<AtomicUsize>,
    abort_calls: Arc<AtomicUsize>,
    active_calls: Arc<AtomicUsize>,
    peak_calls: Arc<AtomicUsize>,
    overlap_barrier: Option<Arc<tokio::sync::Barrier>>,
}

impl PublicProbe {
    fn requiring_overlap(parties: usize) -> Self {
        Self {
            overlap_barrier: Some(Arc::new(tokio::sync::Barrier::new(parties))),
            ..Self::default()
        }
    }
}

#[async_trait]
impl EngineConnection for PublicProbe {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.execute_calls.fetch_add(1, Ordering::SeqCst);
        let active = self.active_calls.fetch_add(1, Ordering::SeqCst) + 1;
        self.peak_calls.fetch_max(active, Ordering::SeqCst);
        if request.query().contains("concurrent") {
            self.overlap_barrier
                .as_ref()
                .expect("concurrent fixtures configure an overlap barrier")
                .wait()
                .await;
        }
        tokio::task::yield_now().await;
        self.active_calls.fetch_sub(1, Ordering::SeqCst);

        if request.query().contains("partial") {
            return Ok(RawResponse::new(ResponseData::Value(json!({
                "partial": {"name": "kept"}
            })))
            .with_errors(vec![GraphQlError::new("field unavailable").with_path(
                vec![
                    GraphQlPathSegment::Field("partial".into()),
                    GraphQlPathSegment::Field("missing".into()),
                ],
            )]));
        }
        if request.query().contains("container") {
            return Ok(RawResponse::new(ResponseData::Value(json!({
                "container": {"id": "ctr-public-fixture"}
            }))));
        }
        Ok(RawResponse::new(ResponseData::Value(
            json!({"version": "v1.0.0"}),
        )))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        self.close_calls.fetch_add(1, Ordering::SeqCst);
        Ok(())
    }

    fn abort(&self) {
        self.abort_calls.fetch_add(1, Ordering::SeqCst);
    }
}

async fn configured_client(probe: PublicProbe) -> dagger_sdk::Client {
    let config = ClientConfig::builder()
        .connection(Box::new(probe))
        .graphql_execution_timeout(Duration::from_secs(1))
        .build()
        .expect("the explicit configuration is valid");
    dagger_sdk::connect_with(config)
        .await
        .expect("an injected connection requires no implicit source")
}

#[test]
fn default_connect_is_exercised_without_inheriting_a_session() {
    let outcome = Command::new(std::env::current_exe().expect("current test executable"))
        .arg("--exact")
        .arg("default_connect_child")
        .arg("--ignored")
        .arg("--nocapture")
        .env(DEFAULT_CONNECT_CHILD, "1")
        .env_remove("DAGGER_SESSION_PORT")
        .env_remove("DAGGER_SESSION_TOKEN")
        .status()
        .expect("isolated default-connect test starts");
    assert!(outcome.success());
}

#[tokio::test]
#[ignore = "run by default_connect_is_exercised_without_inheriting_a_session"]
async fn default_connect_child() {
    if std::env::var_os(DEFAULT_CONNECT_CHILD).is_none() {
        return;
    }
    let failure = match dagger_sdk::connect().await {
        Ok(client) => {
            client.close().await.expect("unexpected client closes");
            return;
        }
        Err(failure) => failure,
    };
    assert!(matches!(
        failure,
        dagger_sdk::ConnectError::Provisioning(_)
            | dagger_sdk::ConnectError::SessionStartup(_)
            | dagger_sdk::ConnectError::Connection(_)
            | dagger_sdk::ConnectError::Compatibility(_)
    ));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn configured_client_unifies_public_query_surfaces_and_close() {
    const CONCURRENT_REQUESTS: usize = 8;
    let probe = PublicProbe::requiring_overlap(CONCURRENT_REQUESTS);
    let client = configured_client(probe.clone()).await;

    let root = client.query();
    assert_eq!(root.version().await.expect("generated request"), "v1.0.0");
    assert_eq!(
        client
            .query_builder()
            .select("version")
            .execute::<String>()
            .await
            .expect("compositional request"),
        "v1.0.0"
    );

    let partial = client
        .execute(RawRequest::new("query { partial { name missing } }"))
        .await
        .expect("partial GraphQL responses are successful protocol responses");
    assert!(
        matches!(partial.data(), ResponseData::Value(value) if value["partial"]["name"] == "kept")
    );
    assert_eq!(partial.errors()[0].path().len(), 2);

    let mut requests = Vec::new();
    for _ in 0..CONCURRENT_REQUESTS {
        let clone = client.clone();
        requests.push(tokio::spawn(async move {
            clone
                .execute(RawRequest::new("query Concurrent { concurrent }"))
                .await
        }));
    }
    for request in requests {
        request
            .await
            .expect("request task joins")
            .expect("concurrent clone executes");
    }
    assert!(probe.peak_calls.load(Ordering::SeqCst) > 1);

    client.close().await.expect("first close succeeds");
    client
        .close()
        .await
        .expect("terminal close result is reusable");
    assert_eq!(probe.close_calls.load(Ordering::SeqCst), 1);
    assert_eq!(probe.abort_calls.load(Ordering::SeqCst), 0);
    assert!(matches!(
        client.execute(RawRequest::new("query { version }")).await,
        Err(RequestError::ClientClosed)
    ));
}

#[tokio::test]
async fn generated_handle_survives_root_and_client_drop_then_cleans_up() {
    let probe = PublicProbe::default();
    let client = configured_client(probe.clone()).await;
    let root = client.query();
    let container = root.container();
    drop(root);
    drop(client);

    assert_eq!(
        container
            .id()
            .await
            .expect("derived handle remains live")
            .into_inner(),
        "ctr-public-fixture"
    );
    assert_eq!(probe.close_calls.load(Ordering::SeqCst), 0);
    drop(container);

    tokio::time::timeout(Duration::from_secs(1), async {
        while probe.close_calls.load(Ordering::SeqCst) != 1 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("final public handle starts cleanup");
    assert_eq!(probe.abort_calls.load(Ordering::SeqCst), 0);
}
