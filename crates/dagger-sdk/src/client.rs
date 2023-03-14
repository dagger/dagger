use std::collections::HashMap;
use std::sync::Arc;

use base64::engine::general_purpose;
use base64::Engine;
use gql_client::ClientConfig;

use dagger_core::config::Config;
use dagger_core::connect_params::ConnectParams;
use dagger_core::engine::Engine as DaggerEngine;

use crate::gen::Query;
use crate::logging::StdLogger;
use crate::querybuilder::query;

pub type DaggerConn = Arc<Query>;

pub async fn connect() -> eyre::Result<DaggerConn> {
    let cfg = Config::new(None, None, None, None, Some(Arc::new(StdLogger::default())));

    connect_opts(cfg).await
}

pub async fn connect_opts(cfg: Config) -> eyre::Result<DaggerConn> {
    let (conn, proc) = DaggerEngine::new().start(&cfg).await?;

    Ok(Arc::new(Query {
        conn,
        proc: Arc::new(proc),
        selection: query(),
    }))
}

pub fn graphql_client(conn: &ConnectParams) -> gql_client::Client {
    let token = general_purpose::URL_SAFE.encode(format!("{}:", conn.session_token));

    let mut headers = HashMap::new();
    headers.insert("Authorization".to_string(), format!("Basic {}", token));

    gql_client::Client::new_with_config(ClientConfig {
        endpoint: conn.url(),
        timeout: Some(1000),
        headers: Some(headers),
        proxy: None,
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
