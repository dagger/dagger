use std::collections::HashMap;
use std::sync::Arc;

use async_trait::async_trait;
use base64::engine::general_purpose;
use base64::Engine;
use thiserror::Error;

use crate::core::connect_params::ConnectParams;
use crate::core::gql_client::{ClientConfig, GQLClient};

use super::config;
use super::gql_client::GraphQLErrorMessage;

#[async_trait]
pub trait GraphQLClient {
    async fn query(&self, query: &str) -> Result<Option<serde_json::Value>, GraphQLError>;
}

pub type DynGraphQLClient = Arc<dyn GraphQLClient + Send + Sync>;

#[derive(Debug)]
pub struct DefaultGraphQLClient {
    client: GQLClient,
}

impl DefaultGraphQLClient {
    pub fn new(conn: &ConnectParams, config: &config::Config) -> Self {
        let token = general_purpose::URL_SAFE.encode(format!("{}:", conn.session_token));

        let mut headers = HashMap::new();
        headers.insert("Authorization".to_string(), format!("Basic {}", token));

        Self {
            client: GQLClient::new_with_config(ClientConfig {
                endpoint: conn.url(),
                connect_timeout_ms: Some(config.timeout_ms),
                execute_timeout_ms: config.execute_timeout_ms,
                headers: Some(headers),
                proxy: None,
            }),
        }
    }
}

#[async_trait]
impl GraphQLClient for DefaultGraphQLClient {
    async fn query(&self, query: &str) -> Result<Option<serde_json::Value>, GraphQLError> {
        let res: Option<serde_json::Value> =
            self.client.query(query).await.map_err(map_graphql_error)?;

        return Ok(res);
    }
}

fn map_graphql_error(gql_error: crate::core::gql_client::GraphQLError) -> GraphQLError {
    let Some(json) = gql_error.json() else {
        return GraphQLError::HttpError(gql_error.message().to_string());
    };

    let Some(message) = json.first().map(|f| f.message.clone()) else {
        return GraphQLError::HttpError(gql_error.message().to_string());
    };

    GraphQLError::DomainError {
        message,
        fields: json,
    }
}

#[derive(Error, Debug)]
pub enum GraphQLError {
    #[error("http error: {0}")]
    HttpError(String),
    #[error("domain error: {message}")]
    DomainError {
        message: String,
        fields: Vec<GraphQLErrorMessage>,
    },
}
