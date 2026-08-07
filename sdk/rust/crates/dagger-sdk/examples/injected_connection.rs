//! Uses a caller-owned transport without consulting an implicit Dagger source.
//!
//! The parent process gives its child malformed ambient session coordinates. The
//! request can therefore succeed only when explicit injection bypasses that source.

use std::process::Command;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

use async_trait::async_trait;
use dagger_sdk::{
    ClientConfig, EngineConnection, EngineConnectionError, RawRequest, RawResponse, ResponseData,
};

#[derive(Clone, Default)]
struct RecordingConnection {
    requests: Arc<AtomicUsize>,
}

#[async_trait]
impl EngineConnection for RecordingConnection {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.requests.fetch_add(1, Ordering::SeqCst);
        Ok(RawResponse::new(ResponseData::Null))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

const INJECTED_CHILD: &str = "DAGGER_RUST_INJECTED_EXAMPLE_CHILD";

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    if std::env::var_os(INJECTED_CHILD).is_none() {
        let status = Command::new(std::env::current_exe()?)
            .env(INJECTED_CHILD, "1")
            .env("DAGGER_SESSION_PORT", "not-a-port")
            .env("DAGGER_SESSION_TOKEN", "must-not-be-read")
            .status()?;
        assert!(status.success());
        return Ok(());
    }

    let connection = RecordingConnection::default();
    let config = ClientConfig::builder()
        .connection(Box::new(connection.clone()))
        .build()?;
    let client = dagger_sdk::connect_with(config).await?;

    client.execute(RawRequest::new("query { version }")).await?;
    client.close().await?;

    assert_eq!(connection.requests.load(Ordering::SeqCst), 1);
    Ok(())
}
