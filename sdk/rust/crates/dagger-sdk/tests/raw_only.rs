//! The stable raw client remains useful when generated bindings are disabled.

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

use async_trait::async_trait;
use dagger_sdk::{
    ClientConfig, EngineConnection, EngineConnectionError, RawRequest, RawResponse, ResponseData,
};

#[derive(Clone, Default)]
struct RawProbe(Arc<AtomicUsize>);

#[async_trait]
impl EngineConnection for RawProbe {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        assert_eq!(request.query(), "query RawOnly { version }");
        self.0.fetch_add(1, Ordering::SeqCst);
        Ok(RawResponse::new(ResponseData::Null))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

#[tokio::test]
async fn injected_raw_execution_needs_no_generated_surface() {
    let probe = RawProbe::default();
    let config = ClientConfig::builder()
        .connection(Box::new(probe.clone()))
        .build()
        .expect("valid raw-only configuration");
    let client = dagger_sdk::connect_with(config)
        .await
        .expect("injected raw-only client");

    let response = client
        .execute(RawRequest::new("query RawOnly { version }"))
        .await
        .expect("raw-only request");
    assert_eq!(response.data(), &ResponseData::Null);
    assert_eq!(probe.0.load(Ordering::SeqCst), 1);
    client.close().await.expect("raw-only close");
}
