use async_trait::async_trait;
use dagger_sdk::{
    Client, EngineConnection, EngineConnectionError, Query, QueryBuilder, RawRequest, RawResponse,
    ResponseData,
};

struct Connection;

#[async_trait]
impl EngineConnection for Connection {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        Ok(RawResponse::new(ResponseData::Null))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

fn assert_send_sync<T: Send + Sync>() {}

fn main() {
    assert_send_sync::<Client>();
    assert_send_sync::<QueryBuilder>();
    assert_send_sync::<Query>();
    let _ = dagger_sdk::ClientConfig::builder()
        .connection(Box::new(Connection))
        .build()
        .unwrap();
}
