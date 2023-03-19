use std::collections::HashMap;
use std::sync::Arc;

use async_trait::async_trait;
use base64::engine::general_purpose;
use base64::Engine;
use gql_client::ClientConfig;

use crate::connect_params::ConnectParams;

#[async_trait]
pub trait GraphQLClient {
    async fn query(&self, query: &str) -> eyre::Result<Option<serde_json::Value>>;
}

pub type DynGraphQLClient = Arc<dyn GraphQLClient + Send + Sync>;

#[derive(Debug)]
pub struct DefaultGraphQLClient {
    client: gql_client::Client,
}

impl DefaultGraphQLClient {
    pub fn new(conn: &ConnectParams) -> Self {
        let token = general_purpose::URL_SAFE.encode(format!("{}:", conn.session_token));

        let mut headers = HashMap::new();
        headers.insert("Authorization".to_string(), format!("Basic {}", token));

        Self {
            client: gql_client::Client::new_with_config(ClientConfig {
                endpoint: conn.url(),
                timeout: Some(1000),
                headers: Some(headers),
                proxy: None,
            }),
        }
    }
}

#[async_trait]
impl GraphQLClient for DefaultGraphQLClient {
    async fn query(&self, query: &str) -> eyre::Result<Option<serde_json::Value>> {
        let res: Option<serde_json::Value> = self
            .client
            .query(&query)
            .await
            .map_err(|r| eyre::anyhow!(r.to_string()))?;

        return Ok(res);
    }
}
