use graphql_client::reqwest::post_graphql_blocking;
use graphql_introspection_query::introspection_response::IntrospectionResponse;
use reqwest::blocking::Client;

use crate::{config::Config, connect_params::ConnectParams};

pub struct Session {}

impl Session {
    pub fn new() -> Self {
        Self {}
    }

    pub fn start(&self, cfg: Config, conn: &ConnectParams) -> eyre::Result<Client> {
        let client = Client::builder()
            .user_agent("graphql-rust/0.10.0")
            .default_headers(
                std::iter::once((
                    reqwest::header::AUTHORIZATION,
                    reqwest::header::HeaderValue::from_str(&format!(
                        "Bearer {}",
                        conn.session_token
                    ))
                    .unwrap(),
                ))
                .collect(),
            )
            .build()?;

        let schema = post_graphql_blocking::<IntrospectionResponse, _>(&client, conn.url(), vec![]);

        Ok(client)
    }
}
