use std::sync::Arc;

use crate::core::config::Config;
use crate::core::engine::Engine as DaggerEngine;
use crate::core::graphql_client::DefaultGraphQLClient;

use crate::errors::ConnectError;
use crate::gen::Query;
use crate::logging::StdLogger;
use crate::querybuilder::query;

pub type DaggerConn = Query;

pub async fn connect<F, Fut>(dagger: F) -> Result<(), ConnectError>
where
    F: FnOnce(DaggerConn) -> Fut + 'static,
    Fut: futures::Future<Output = eyre::Result<()>> + 'static,
{
    let cfg = Config::new(None, None, None, None, Some(Arc::new(StdLogger::default())));

    connect_opts(cfg, dagger).await
}

pub async fn connect_opts<F, Fut>(cfg: Config, dagger: F) -> Result<(), ConnectError>
where
    F: FnOnce(DaggerConn) -> Fut + 'static,
    Fut: futures::Future<Output = eyre::Result<()>> + 'static,
{
    let (conn, proc) = DaggerEngine::new()
        .start(&cfg)
        .await
        .map_err(ConnectError::FailedToConnect)?;

    let proc = proc.map(Arc::new);

    let client = Query {
        proc: proc.clone(),
        selection: query(),
        graphql_client: Arc::new(DefaultGraphQLClient::new(&conn)),
    };

    dagger(client).await.map_err(ConnectError::DaggerContext)?;

    if let Some(proc) = &proc {
        proc.shutdown()
            .await
            .map_err(ConnectError::FailedToShutdown)?;
    }

    Ok(())
}

// Conn will automatically close on drop of proc

#[cfg(test)]
mod test {
    use super::connect;

    #[tokio::test]
    async fn test_connect() -> eyre::Result<()> {
        tracing_subscriber::fmt::init();

        connect(|client| async move {
            client
                .container()
                .from("alpine:latest")
                .with_exec(vec!["echo", "1"])
                .sync()
                .await?;

            Ok(())
        })
        .await?;

        Ok(())
    }
}
