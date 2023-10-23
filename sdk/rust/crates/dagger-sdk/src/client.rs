use std::sync::Arc;

use crate::core::config::Config;
use crate::core::engine::Engine as DaggerEngine;
use crate::core::graphql_client::DefaultGraphQLClient;

use crate::errors::ConnectError;
use crate::gen::Query;
use crate::logging::StdLogger;
use crate::querybuilder::query;

pub type DaggerConn = Query;

pub async fn connect() -> Result<DaggerConn, ConnectError> {
    let cfg = Config::new(None, None, None, None, Some(Arc::new(StdLogger::default())));

    connect_opts(cfg).await
}

pub async fn connect_opts(cfg: Config) -> Result<DaggerConn, ConnectError> {
    let (conn, proc) = DaggerEngine::new()
        .start(&cfg)
        .await
        .map_err(ConnectError::FailedToConnect)?;

    Ok(Query {
        proc: proc.map(Arc::new),
        selection: query(),
        graphql_client: Arc::new(DefaultGraphQLClient::new(&conn)),
    })
}

// Conn will automatically close on drop of proc

#[cfg(test)]
mod test {
    use super::connect;

    #[tokio::test]
    async fn test_connect() {
        let _ = connect().await.unwrap();
    }
}
