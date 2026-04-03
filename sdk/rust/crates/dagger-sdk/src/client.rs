use std::sync::Arc;

use crate::core::config::Config;
use crate::core::engine::Engine as DaggerEngine;
use crate::core::graphql_client::DefaultGraphQLClient;

use crate::errors::{ConnectError, DaggerError};
use crate::gen::{Id, Query};
use crate::id::IntoID;
use crate::loadable::Loadable;
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

impl Query {
    /// Return a lazy reference to a node by its ID without making a
    /// network call. The returned value can be used to chain further
    /// queries.
    ///
    /// ```ignore
    /// let ctr: Container = client.r#ref(id);
    /// let out = ctr.with_exec(vec!["echo", "hi"]).stdout().await?;
    /// ```
    pub fn r#ref<T: Loadable>(&self, id: impl IntoID<Id>) -> T {
        let selection = self
            .selection
            .select("node")
            .arg_lazy(
                "id",
                Box::new(move || {
                    let id = id.clone();
                    Box::pin(async move {
                        let resolved = id.into_id().await.unwrap();
                        format!("\"{}\"", resolved.0)
                    })
                }),
            )
            .inline_fragment(T::graphql_type());

        T::from_query(self.proc.clone(), selection, self.graphql_client.clone())
    }

    /// Load a node by its ID with type safety. Verifies the node
    /// exists and matches the expected type before returning.
    ///
    /// ```ignore
    /// let ctr: Container = client.load(id).await?;
    /// ```
    pub async fn load<T: Loadable>(&self, id: impl IntoID<Id>) -> Result<T, DaggerError> {
        let type_name = T::graphql_type();

        // Verify the node exists by querying __typename through the
        // inline fragment. If the concrete type doesn't match the
        // fragment, the response will be empty.
        let check_selection = self
            .selection
            .select("node")
            .arg_lazy("id", {
                let id = id.clone();
                Box::new(move || {
                    let id = id.clone();
                    Box::pin(async move {
                        let resolved = id.into_id().await.unwrap();
                        format!("\"{}\"", resolved.0)
                    })
                })
            })
            .inline_fragment(type_name)
            .select("id");

        let _: Id = check_selection.execute(self.graphql_client.clone()).await?;

        Ok(self.r#ref(id))
    }
}

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
