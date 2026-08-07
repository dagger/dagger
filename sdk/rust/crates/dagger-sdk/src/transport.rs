//! Authenticated GraphQL transport confined to one validated IPv4 loopback session.
//!
//! The endpoint authority and path are constructed internally, proxy use and redirects
//! are disabled, and an execution builds exactly one Reqwest request. Connection
//! establishment policy therefore cannot turn an ambiguous body delivery into replay.

use std::fmt;
use std::time::Duration;

use async_trait::async_trait;

use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
use crate::graphql::{RawRequest, RawResponse};
use crate::preflight::PropagationEnvironment;
use crate::propagation::W3cPropagation;
use crate::session::{ExistingSessionResource, SecretString};

/// Compile-time factory boundary for the concrete connector and transport fixtures.
pub(crate) trait LoopbackTransportFactory: Send + Sync {
    type Connection: EngineConnection;

    fn loopback(
        &self,
        port: u16,
        token: SecretString,
        connect_timeout: Duration,
        inherited: PropagationEnvironment,
        external_resource: Option<ExistingSessionResource>,
    ) -> Result<Self::Connection, EngineConnectionError>;
}

/// Production factory for proxy-free, non-redirecting Reqwest connections.
#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct ReqwestLoopbackFactory;

impl LoopbackTransportFactory for ReqwestLoopbackFactory {
    type Connection = ReqwestLoopbackConnection;

    fn loopback(
        &self,
        port: u16,
        token: SecretString,
        connect_timeout: Duration,
        inherited: PropagationEnvironment,
        external_resource: Option<ExistingSessionResource>,
    ) -> Result<Self::Connection, EngineConnectionError> {
        ReqwestLoopbackConnection::new(port, token, connect_timeout, inherited, external_resource)
    }
}

/// One private authenticated connection to a validated Dagger session.
pub(crate) struct ReqwestLoopbackConnection {
    client: reqwest::Client,
    endpoint: reqwest::Url,
    token: SecretString,
    propagation: W3cPropagation,
    external_resource: Option<ExistingSessionResource>,
}

impl ReqwestLoopbackConnection {
    fn new(
        port: u16,
        token: SecretString,
        connect_timeout: Duration,
        inherited: PropagationEnvironment,
        external_resource: Option<ExistingSessionResource>,
    ) -> Result<Self, EngineConnectionError> {
        let endpoint =
            reqwest::Url::parse(&format!("http://127.0.0.1:{port}/query")).map_err(|error| {
                EngineConnectionError::with_source(EngineConnectionErrorKind::Protocol, error)
            })?;
        let client = reqwest::Client::builder()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .connect_timeout(connect_timeout)
            .build()
            .map_err(|error| {
                EngineConnectionError::with_source(EngineConnectionErrorKind::Transport, error)
            })?;
        Ok(Self {
            client,
            endpoint,
            token,
            propagation: W3cPropagation::new(inherited),
            external_resource,
        })
    }

    #[cfg(test)]
    pub(crate) fn endpoint_for_test(&self) -> &reqwest::Url {
        &self.endpoint
    }
}

impl fmt::Debug for ReqwestLoopbackConnection {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ReqwestLoopbackConnection")
            .field("authority", &"127.0.0.1")
            .field("port", &self.endpoint.port())
            .field("path", &self.endpoint.path())
            .field("token", &self.token)
            .field("externally_owned", &self.external_resource.is_some())
            .finish()
    }
}

#[async_trait]
impl EngineConnection for ReqwestLoopbackConnection {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        let body = request.encode_wire().map_err(|error| {
            EngineConnectionError::with_source(EngineConnectionErrorKind::Protocol, error)
        })?;
        let headers = self.propagation.request_headers().map_err(|error| {
            EngineConnectionError::with_source(EngineConnectionErrorKind::Protocol, error)
        })?;

        // There is deliberately one builder and one `send` call. Redirect policy is
        // disabled at the client, and errors after this point are terminal for this
        // execution because delivery may already have occurred.
        let response = self
            .client
            .post(self.endpoint.clone())
            .headers(headers)
            .basic_auth(self.token.expose_for_authentication(), Some(""))
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .body(body)
            .send()
            .await
            .map_err(|error| {
                let kind = if error.is_connect() && error.is_timeout() {
                    EngineConnectionErrorKind::ConnectTimeout
                } else {
                    EngineConnectionErrorKind::Transport
                };
                EngineConnectionError::with_source(kind, error)
            })?;
        let status = response.status();
        let bytes = response.bytes().await.map_err(|error| {
            EngineConnectionError::with_source(EngineConnectionErrorKind::Transport, error)
        })?;

        if !status.is_success() {
            // A valid GraphQL failure body is valuable evidence, but an invalid body
            // cannot erase the independently known HTTP status classification.
            let decoded = RawResponse::decode_wire(&bytes).ok();
            return Err(EngineConnectionError::with_http_response(
                status.as_u16(),
                decoded,
            ));
        }

        RawResponse::decode_wire(&bytes).map_err(|error| {
            EngineConnectionError::with_source(EngineConnectionErrorKind::Protocol, error)
        })
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        if let Some(resource) = self.external_resource {
            // Existing sessions are external capabilities. Closing this transport must
            // never signal an engine which may still serve another SDK client.
            resource.close();
        }
        Ok(())
    }

    fn abort(&self) {
        if let Some(resource) = self.external_resource {
            resource.abort();
        }
    }
}
