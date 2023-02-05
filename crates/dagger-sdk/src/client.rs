use std::collections::HashMap;
use std::sync::Arc;

use base64::engine::general_purpose;
use base64::Engine;
use dagger_core::config::Config;
use dagger_core::connect_params::ConnectParams;
use dagger_core::engine::Engine as DaggerEngine;
use gql_client::ClientConfig;

use crate::gen::Query;
use crate::querybuilder::query;

pub type DaggerConn = Arc<Query>;

pub fn connect() -> eyre::Result<DaggerConn> {
    let cfg = Config::default();
    let (conn, proc) = DaggerEngine::new().start(&cfg)?;

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
        timeout: None,
        headers: Some(headers),
        proxy: None,
    })
}

// Conn will automatically close on drop of proc

#[cfg(test)]
mod test {
    use super::connect;

    #[test]
    fn test_connect() {
        let _ = connect().unwrap();
    }
}
