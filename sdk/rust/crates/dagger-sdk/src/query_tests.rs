//! Shared-session query tests keep recording machinery inside the crate boundary.

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

use async_trait::async_trait;
use proptest::prelude::*;

use crate::config::ClientConfig;
use crate::connection::{EngineConnection, EngineConnectionError};
use crate::graphql::{RawRequest, RawResponse, ResponseData};
use crate::test_support::proptest_config;

#[derive(Clone, Default)]
struct QueryProbe {
    calls: Arc<AtomicUsize>,
    active: Arc<AtomicUsize>,
    peak: Arc<AtomicUsize>,
    overlap_barrier: Option<Arc<tokio::sync::Barrier>>,
}

impl QueryProbe {
    fn requiring_overlap(parties: usize) -> Self {
        Self {
            overlap_barrier: Some(Arc::new(tokio::sync::Barrier::new(parties))),
            ..Self::default()
        }
    }
}

#[async_trait]
impl EngineConnection for QueryProbe {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        let active = self.active.fetch_add(1, Ordering::SeqCst) + 1;
        self.peak.fetch_max(active, Ordering::SeqCst);
        if let Some(barrier) = &self.overlap_barrier {
            // A scheduler may immediately repoll after yield_now. The barrier makes
            // overlap a fixture invariant instead of a wall-clock or fairness guess.
            barrier.wait().await;
        }
        tokio::task::yield_now().await;
        self.active.fetch_sub(1, Ordering::SeqCst);
        Ok(RawResponse::new(ResponseData::Value(
            serde_json::json!({"version": "v1.0.0"}),
        )))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

async fn client_with(probe: QueryProbe) -> crate::Client {
    let config = ClientConfig::builder()
        .connection(Box::new(probe))
        .build()
        .expect("valid explicit connection");
    crate::connect_with(config)
        .await
        .expect("explicit connection bypasses source discovery")
}

proptest! {
    #![proptest_config(proptest_config())]

    // Invariant: every derived public handle remains a lease on the original session.
    // Feature: rust-sdk-client-lifecycle, Property 3: handles share exactly one session
    #[test]
    fn handles_share_exactly_one_session(
        client_clones in 0_usize..16,
        builder_clones in 0_usize..16,
        generated_derivations in 0_usize..16,
        drop_prefix in 0_usize..16,
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let probe = QueryProbe::default();
            let client = client_with(probe.clone()).await;
            let identity = client.session_identity();

            let mut clients = (0..client_clones)
                .map(|_| client.clone())
                .collect::<Vec<_>>();
            let mut builders = (0..builder_clones)
                .map(|_| client.query_builder())
                .collect::<Vec<_>>();
            let roots = (0..generated_derivations)
                .map(|_| client.query())
                .collect::<Vec<_>>();
            let containers = roots.iter().map(crate::Query::container).collect::<Vec<_>>();

            prop_assert!(clients.iter().all(|value| value.session_identity() == identity));
            prop_assert!(builders.iter().all(|value| value.session_identity() == identity));
            prop_assert!(roots.iter().all(|value| value.session.identity() == identity));
            prop_assert!(containers.iter().all(|value| value.session.identity() == identity));
            prop_assert_eq!(probe.calls.load(Ordering::SeqCst), 0);

            let client_drop = drop_prefix.min(clients.len());
            clients.drain(..client_drop);
            let builder_drop = drop_prefix.min(builders.len());
            builders.drain(..builder_drop);
            let value: String = client
                .query_builder()
                .select("version")
                .execute()
                .await
                .expect("remaining lease executes");
            prop_assert_eq!(value, "v1.0.0");
            prop_assert_eq!(probe.calls.load(Ordering::SeqCst), 1);
            client.close().await.expect("probe close");
            Ok(())
        })?;
    }

    // Invariant: raw, generated, and compositional requests meet at one concurrent executor.
    // Feature: rust-sdk-client-lifecycle, Property 20: every query surface uses the same session concurrently
    #[test]
    fn every_query_surface_uses_the_same_session_concurrently(
        raw_count in 1_usize..8,
        generated_count in 1_usize..8,
        compositional_count in 1_usize..8,
    ) {
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let total = raw_count + generated_count + compositional_count;
            let probe = QueryProbe::requiring_overlap(total);
            let client = client_with(probe.clone()).await;

            let document = client
                .query_builder()
                .select("version")
                .document()
                .await
                .expect("pure query construction");
            prop_assert_eq!(document, "query{version}");
            prop_assert_eq!(probe.calls.load(Ordering::SeqCst), 0);

            let mut tasks = Vec::new();
            for _ in 0..raw_count {
                let lease = client.clone();
                tasks.push(tokio::spawn(async move {
                    lease
                        .execute(RawRequest::new("query{version}"))
                        .await
                        .map(|_| ())
                        .map_err(|_| ())
                }));
            }
            for _ in 0..generated_count {
                let root = client.query();
                tasks.push(tokio::spawn(async move {
                    root.version().await.map(|_: String| ()).map_err(|_| ())
                }));
            }
            for _ in 0..compositional_count {
                let builder = client.query_builder().select("version");
                tasks.push(tokio::spawn(async move {
                    builder.execute::<String>().await.map(|_| ()).map_err(|_| ())
                }));
            }
            for task in tasks {
                task.await.expect("query task").expect("query succeeds");
            }

            prop_assert_eq!(probe.calls.load(Ordering::SeqCst), total);
            prop_assert!(probe.peak.load(Ordering::SeqCst) > 1);
            client.close().await.expect("probe close");
            Ok(())
        })?;
    }
}
