use std::sync::Arc;

use dagger_core::graphql_client::DefaultGraphQLClient;

use dagger_core::config::Config;
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
        proc: Arc::new(proc),
        selection: query(),
        graphql_client: Arc::new(DefaultGraphQLClient::new(&conn)),
    }))
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
