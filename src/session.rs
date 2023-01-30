use dagger_core::introspection::{self, IntrospectionResponse};
use graphql_client::GraphQLQuery;
use reqwest::{
    blocking::{Client, RequestBuilder},
    header::{HeaderMap, HeaderValue, ACCEPT, CONTENT_TYPE},
};

use crate::{config::Config, connect_params::ConnectParams};

#[derive(GraphQLQuery)]
#[graphql(
    schema_path = "src/graphql/introspection_schema.graphql",
    query_path = "src/graphql/introspection_query.graphql",
    responsive_path = "Serialize",
    variable_derive = "Deserialize"
)]
struct IntrospectionQuery;

pub struct Session;

impl Session {
    pub fn new() -> Self {
        Self {}
    }

    pub fn start(&self, _cfg: &Config, conn: &ConnectParams) -> eyre::Result<RequestBuilder> {
        let client = Client::builder()
            .user_agent("graphql-rust/0.10.0")
            .connection_verbose(true)
            //.danger_accept_invalid_certs(true)
            .build()?;

        let req_builder = client
            .post(conn.url())
            .headers(construct_headers())
            .basic_auth::<String, String>(conn.session_token.to_string(), None);

        Ok(req_builder)
    }

    pub fn schema(&self, req_builder: RequestBuilder) -> eyre::Result<IntrospectionResponse> {
        let request_body: graphql_client::QueryBody<()> = graphql_client::QueryBody {
            variables: (),
            query: introspection_query::QUERY,
            operation_name: introspection_query::OPERATION_NAME,
        };

        let res = req_builder.json(&request_body).send()?;

        if res.status().is_success() {
            // do nothing
        } else if res.status().is_server_error() {
            return Err(eyre::anyhow!("server error!"));
        } else {
            let status = res.status();
            let error_message = match res.text() {
                Ok(msg) => match serde_json::from_str::<serde_json::Value>(&msg) {
                    Ok(json) => {
                        format!("HTTP {}\n{}", status, serde_json::to_string_pretty(&json)?)
                    }
                    Err(_) => format!("HTTP {}: {}", status, msg),
                },
                Err(_) => format!("HTTP {}", status),
            };
            return Err(eyre::anyhow!(error_message));
        }

        let json: introspection::IntrospectionResponse = res.json()?;

        Ok(json)
    }
}

fn construct_headers() -> HeaderMap {
    let mut headers = HeaderMap::new();
    headers.insert(CONTENT_TYPE, HeaderValue::from_static("application/json"));
    headers.insert(ACCEPT, HeaderValue::from_static("application/json"));
    headers
}
