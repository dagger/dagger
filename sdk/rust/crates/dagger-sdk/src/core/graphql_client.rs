use std::collections::HashMap;
use std::sync::Arc;

use async_trait::async_trait;
use base64::engine::general_purpose;
use base64::Engine;
use thiserror::Error;

use crate::core::connect_params::ConnectParams;
use crate::core::gql_client::{ClientConfig, GQLClient};

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
    pub fn new(conn: &ConnectParams) -> Self {
        let token = general_purpose::URL_SAFE.encode(format!("{}:", conn.session_token));

        let mut headers = HashMap::new();
        headers.insert("Authorization".to_string(), format!("Basic {}", token));

        Self {
            client: GQLClient::new_with_config(ClientConfig {
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
    async fn query(&self, query: &str) -> Result<Option<serde_json::Value>, GraphQLError> {
        let res: Option<serde_json::Value> =
            self.client.query(&query).await.map_err(map_graphql_error)?;

        return Ok(res);
    }
}

fn map_graphql_error(gql_error: crate::core::gql_client::GraphQLError) -> GraphQLError {
    let message = gql_error.message().to_string();
    let json = gql_error.json();

    if let Some(json) = json {
        if !json.is_empty() {
            return GraphQLError::DomainError {
                message,
                fields: GraphqlErrorMessages(json.into_iter().map(|e| e.message).collect()),
            };
        }
    }

    GraphQLError::HttpError(message)
}

#[derive(Error, Debug)]
pub enum GraphQLError {
    #[error("http error: {0}")]
    HttpError(String),
    #[error("domain error:\n{message}\n{fields}")]
    DomainError {
        message: String,
        fields: GraphqlErrorMessages,
    },
}

#[derive(Debug, Clone)]
pub struct GraphqlErrorMessages(Vec<String>);

impl std::fmt::Display for GraphqlErrorMessages {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        for error in self.0.iter() {
            f.write_fmt(format_args!("{error}\n"))?;
        }

        Ok(())
    }
}
