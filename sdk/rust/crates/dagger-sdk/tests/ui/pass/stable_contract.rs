use std::sync::Arc;

use async_trait::async_trait;
use dagger_sdk::{
    ClientConfig, Diagnostic, DiagnosticSink, DiagnosticSinkError, EngineConnection,
    EngineConnectionError, QueryError, RawRequest, RawResponse, ResponseData,
};

struct Sink;

impl DiagnosticSink for Sink {
    fn emit(&self, _diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
        Ok(())
    }
}

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

fn inspect(error: QueryError) {
    match error {
        QueryError::Exec { error, response } => {
            let _ = (error.exit_code(), error.command(), error.extensions(), response.data());
        }
        QueryError::GraphQl { response } => {
            let _ = response.errors();
        }
        _ => {}
    }
}

fn main() {
    let _implicit = ClientConfig::builder()
        .diagnostic_sink(Arc::new(Sink))
        .allow_unverified_compatibility(false)
        .build()
        .unwrap();
    let _explicit = ClientConfig::builder()
        .connection(Box::new(Connection))
        .build()
        .unwrap();
    let _ = inspect;
}
