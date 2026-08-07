//! Owned public client facade over one shared raw/generated query session.
//!
//! The stable facade is a cloneable lease on one shared session. Its raw execution and
//! close behavior remain available with generated bindings disabled. With `gen`, root
//! query construction clones the same lease and performs no transport work.

use crate::config::ClientConfig;
use crate::connector::{ConnectionRequest, Connector, DefaultConnector};
use crate::errors::{CloseError, ConnectError, RequestError};
use crate::graphql::{RawRequest, RawResponse};
use crate::lifecycle::SessionHandle;
use crate::preflight::{ConnectionPlan, preflight};
use crate::query::QueryBuilder;

/// A cloneable owned lease on one Dagger engine session.
///
/// Cloning a client does not open another connection. Explicit [`Client::close`] calls
/// across all clones share one terminal result; dropping the final clone starts
/// non-blocking cleanup when close was not called.
#[derive(Clone)]
pub struct Client {
    session: SessionHandle,
}

impl Client {
    /// Starts an immutable compositional query on this client's shared session.
    ///
    /// Construction performs no I/O; the returned builder is an independent session
    /// lease and remains usable if this `Client` value is dropped.
    pub fn query_builder(&self) -> QueryBuilder {
        QueryBuilder::new(self.session.clone())
    }

    /// Returns a fresh generated root query on this client's shared session.
    ///
    /// Root construction performs no I/O. Generated handles derived from the root own
    /// independent leases, so dropping either value cannot invalidate the other.
    #[cfg(feature = "gen")]
    pub fn query(&self) -> crate::Query {
        crate::Query {
            session: self.session.clone(),
            selection: crate::query::query(),
        }
    }

    /// Executes one raw GraphQL request through this client's shared session.
    ///
    /// Requests admitted before close may finish or return
    /// [`RequestError::InterruptedByClose`]. Once close begins, new requests fail with
    /// [`RequestError::ClientClosed`] without invoking the connection.
    pub async fn execute(&self, request: RawRequest) -> Result<RawResponse, RequestError> {
        self.session.execute(request).await
    }

    /// Gracefully closes the shared session and returns its reusable terminal result.
    ///
    /// This method is cancellation-safe: abandoning one waiter does not cancel the
    /// SDK-owned shutdown attempt, and a later caller observes the same result.
    pub async fn close(&self) -> Result<(), CloseError> {
        self.session.close().await
    }

    #[cfg(test)]
    pub(crate) fn lifecycle_state(&self) -> &'static str {
        self.session.lifecycle_state()
    }

    #[cfg(test)]
    pub(crate) fn session_identity(&self) -> usize {
        self.session.identity()
    }
}

/// Connects with the documented default configuration.
pub async fn connect() -> Result<Client, ConnectError> {
    connect_with(ClientConfig::default()).await
}

/// Consumes a validated configuration and returns one owned client.
pub async fn connect_with(config: ClientConfig) -> Result<Client, ConnectError> {
    connect_with_connector(config, &DefaultConnector).await
}

pub(crate) async fn connect_with_connector(
    config: ClientConfig,
    connector: &dyn Connector,
) -> Result<Client, ConnectError> {
    let plan = preflight(config)?;
    if let ConnectionPlan::Explicit {
        connection,
        execution_timeout,
    } = plan
    {
        // The explicit path intentionally returns before constructing a connector
        // request. This preserves caller injection as a complete abstraction and
        // prevents environment, discovery, process, or network side effects.
        return Ok(Client {
            session: SessionHandle::new(connection, None, execution_timeout),
        });
    }

    let request = ConnectionRequest::try_from(plan).map_err(|_| internal_connect_error())?;
    let (startup_timeout, http_connect_timeout, execution_timeout) = request.timeouts();
    let connection = tokio::time::timeout(startup_timeout, connector.connect(request))
        .await
        .map_err(|_| ConnectError::StartupTimeout {
            duration: startup_timeout,
        })??;

    Ok(Client {
        session: SessionHandle::new(connection, Some(http_connect_timeout), execution_timeout),
    })
}

fn internal_connect_error() -> ConnectError {
    ConnectError::Connection(crate::EngineConnectionError::new(
        crate::EngineConnectionErrorKind::Other,
    ))
}

#[cfg(feature = "gen")]
use crate::errors::{QueryBuildError, QueryBuildErrorKind, QueryError};
#[cfg(feature = "gen")]
use crate::r#gen::{Id, Query};
#[cfg(feature = "gen")]
use crate::id::IntoID;
#[cfg(feature = "gen")]
use crate::loadable::Loadable;

#[cfg(feature = "gen")]
impl Query {
    /// Returns a lazy reference to a node by its ID without making a network call.
    ///
    /// The returned value can be used to chain further queries.
    ///
    /// ```no_run
    /// # async fn example() -> Result<(), Box<dyn std::error::Error>> {
    /// # let client = dagger_sdk::connect().await?;
    /// # let query = client.query();
    /// # let id = query.container().id().await?;
    /// let ctr: dagger_sdk::Container = query.r#ref(id);
    /// let out = ctr.with_exec(vec!["echo", "hi"]).stdout().await?;
    /// # client.close().await?;
    /// # Ok(())
    /// # }
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
                        id.into_id()
                            .await
                            .map(|resolved| format!("\"{}\"", resolved.0))
                            .map_err(|error| {
                                QueryBuildError::with_source(
                                    QueryBuildErrorKind::LazyIdentifier,
                                    error,
                                )
                            })
                    })
                }),
            )
            .inline_fragment(<T as crate::loadable::private::Sealed>::graphql_type());

        <T as crate::loadable::private::Sealed>::from_query(self.session.clone(), selection)
    }

    /// Loads a node by ID after verifying that it exists with the expected type.
    pub async fn load<T: Loadable>(&self, id: impl IntoID<Id>) -> Result<T, QueryError> {
        let type_name = <T as crate::loadable::private::Sealed>::graphql_type();
        // Asking for an ID through a concrete inline fragment makes a missing or
        // mismatched node fail before constructing the caller's typed handle.
        let check_selection = self
            .selection
            .select("node")
            .arg_lazy("id", {
                let id = id.clone();
                Box::new(move || {
                    let id = id.clone();
                    Box::pin(async move {
                        id.into_id()
                            .await
                            .map(|resolved| format!("\"{}\"", resolved.0))
                            .map_err(|error| {
                                QueryBuildError::with_source(
                                    QueryBuildErrorKind::LazyIdentifier,
                                    error,
                                )
                            })
                    })
                })
            })
            .inline_fragment(type_name)
            .select("id");

        let _: Id = check_selection.execute(&self.session).await?;
        Ok(self.r#ref(id))
    }
}
