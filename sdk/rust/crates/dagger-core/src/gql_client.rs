use reqwest::Error;
use reqwest::{Client, Url};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fmt::{self, Formatter};
use std::str::FromStr;

#[derive(Clone)]
pub struct GraphQLError {
    message: String,
    json: Option<Vec<GraphQLErrorMessage>>,
}

// https://spec.graphql.org/June2018/#sec-Errors
#[derive(Deserialize, Debug, Clone)]
#[allow(dead_code)]
pub struct GraphQLErrorMessage {
    pub message: String,
    locations: Option<Vec<GraphQLErrorLocation>>,
    extensions: Option<HashMap<String, String>>,
    path: Option<Vec<GraphQLErrorPathParam>>,
}

#[derive(Deserialize, Debug, Clone)]
#[allow(dead_code)]
pub struct GraphQLErrorLocation {
    line: u32,
    column: u32,
}

#[derive(Deserialize, Debug, Clone)]
#[serde(untagged)]
pub enum GraphQLErrorPathParam {
    String(String),
    Number(u32),
}

impl GraphQLError {
    pub fn with_text(message: impl AsRef<str>) -> Self {
        Self {
            message: message.as_ref().to_string(),
            json: None,
        }
    }

    pub fn with_message_and_json(message: impl AsRef<str>, json: Vec<GraphQLErrorMessage>) -> Self {
        Self {
            message: message.as_ref().to_string(),
            json: Some(json),
        }
    }

    pub fn with_json(json: Vec<GraphQLErrorMessage>) -> Self {
        Self::with_message_and_json("Look at json field for more details", json)
    }

    pub fn message(&self) -> &str {
        &self.message
    }

    pub fn json(&self) -> Option<Vec<GraphQLErrorMessage>> {
        self.json.clone()
    }
}

fn format(err: &GraphQLError, f: &mut Formatter<'_>) -> fmt::Result {
    // Print the main error message
    writeln!(f, "\nGQLClient Error: {}", err.message)?;

    // Check if query errors have been received
    if err.json.is_none() {
        return Ok(());
    }

    let errors = err.json.as_ref();

    for err in errors.unwrap() {
        writeln!(f, "Message: {}", err.message)?;
    }

    Ok(())
}

impl fmt::Display for GraphQLError {
    fn fmt(&self, f: &mut Formatter<'_>) -> fmt::Result {
        format(self, f)
    }
}

impl fmt::Debug for GraphQLError {
    #[allow(clippy::needless_borrow)]
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        format(&self, f)
    }
}

impl From<Error> for GraphQLError {
    fn from(error: Error) -> Self {
        Self {
            message: error.to_string(),
            json: None,
        }
    }
}

/// GQL client config
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ClientConfig {
    /// the endpoint about graphql server
    pub endpoint: String,
    /// gql query timeout, unit: seconds
    pub timeout: Option<u64>,
    /// additional request header
    pub headers: Option<HashMap<String, String>>,
    /// request proxy
    pub proxy: Option<GQLProxy>,
}

/// proxy type
#[derive(Clone, Debug, Deserialize, Serialize)]
pub enum ProxyType {
    Http,
    Https,
    All,
}

/// proxy auth, basic_auth
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ProxyAuth {
    pub username: String,
    pub password: String,
}

/// request proxy
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct GQLProxy {
    /// schema, proxy url
    pub schema: String,
    /// proxy type
    pub type_: ProxyType,
    /// auth
    pub auth: Option<ProxyAuth>,
}

#[cfg(not(target_arch = "wasm32"))]
impl TryFrom<GQLProxy> for reqwest::Proxy {
    type Error = GraphQLError;

    fn try_from(gql_proxy: GQLProxy) -> Result<Self, Self::Error> {
        let proxy = match gql_proxy.type_ {
            ProxyType::Http => reqwest::Proxy::http(gql_proxy.schema),
            ProxyType::Https => reqwest::Proxy::https(gql_proxy.schema),
            ProxyType::All => reqwest::Proxy::all(gql_proxy.schema),
        }
        .map_err(|e| Self::Error::with_text(format!("{:?}", e)))?;
        Ok(proxy)
    }
}

#[derive(Clone, Debug)]
pub struct GQLClient {
    config: ClientConfig,
}

#[derive(Serialize)]
struct RequestBody<T: Serialize> {
    query: String,
    variables: T,
}

#[derive(Deserialize, Debug)]
struct GraphQLResponse<T> {
    data: Option<T>,
    errors: Option<Vec<GraphQLErrorMessage>>,
}

impl GQLClient {
    fn client(&self) -> Result<Client, GraphQLError> {
        let mut builder = Client::builder().timeout(std::time::Duration::from_secs(
            self.config.timeout.unwrap_or(5),
        ));
        if let Some(proxy) = &self.config.proxy {
            builder = builder.proxy(proxy.clone().try_into()?);
        }
        builder
            .build()
            .map_err(|e| GraphQLError::with_text(format!("Can not create client: {:?}", e)))
    }
}

impl GQLClient {
    pub fn new(endpoint: impl AsRef<str>) -> Self {
        Self {
            config: ClientConfig {
                endpoint: endpoint.as_ref().to_string(),
                timeout: None,
                headers: Default::default(),
                proxy: None,
            },
        }
    }

    pub fn new_with_headers(
        endpoint: impl AsRef<str>,
        headers: HashMap<impl ToString, impl ToString>,
    ) -> Self {
        let _headers: HashMap<String, String> = headers
            .iter()
            .map(|(name, value)| (name.to_string(), value.to_string()))
            .into_iter()
            .collect();
        Self {
            config: ClientConfig {
                endpoint: endpoint.as_ref().to_string(),
                timeout: None,
                headers: Some(_headers),
                proxy: None,
            },
        }
    }

    pub fn new_with_config(config: ClientConfig) -> Self {
        Self { config }
    }
}

impl GQLClient {
    pub async fn query<K>(&self, query: &str) -> Result<Option<K>, GraphQLError>
    where
        K: for<'de> Deserialize<'de>,
    {
        self.query_with_vars::<K, ()>(query, ()).await
    }

    pub async fn query_unwrap<K>(&self, query: &str) -> Result<K, GraphQLError>
    where
        K: for<'de> Deserialize<'de>,
    {
        self.query_with_vars_unwrap::<K, ()>(query, ()).await
    }

    pub async fn query_with_vars_unwrap<K, T: Serialize>(
        &self,
        query: &str,
        variables: T,
    ) -> Result<K, GraphQLError>
    where
        K: for<'de> Deserialize<'de>,
    {
        match self.query_with_vars(query, variables).await? {
            Some(v) => Ok(v),
            None => Err(GraphQLError::with_text(format!(
                "No data from graphql server({}) for this query",
                self.config.endpoint
            ))),
        }
    }

    pub async fn query_with_vars<K, T: Serialize>(
        &self,
        query: &str,
        variables: T,
    ) -> Result<Option<K>, GraphQLError>
    where
        K: for<'de> Deserialize<'de>,
    {
        self.query_with_vars_by_endpoint(&self.config.endpoint, query, variables)
            .await
    }

    async fn query_with_vars_by_endpoint<K, T: Serialize>(
        &self,
        endpoint: impl AsRef<str>,
        query: &str,
        variables: T,
    ) -> Result<Option<K>, GraphQLError>
    where
        K: for<'de> Deserialize<'de>,
    {
        let mut times = 1;
        let mut endpoint = endpoint.as_ref().to_string();
        let endpoint_url = Url::from_str(&endpoint).map_err(|e| {
            GraphQLError::with_text(format!("Wrong endpoint: {}. {:?}", endpoint, e))
        })?;
        let schema = endpoint_url.scheme();
        let host = endpoint_url
            .host()
            .ok_or_else(|| GraphQLError::with_text(format!("Wrong endpoint: {}", endpoint)))?;

        let client: Client = self.client()?;
        let body = RequestBody {
            query: query.to_string(),
            variables,
        };

        loop {
            if times > 10 {
                return Err(GraphQLError::with_text(format!(
                    "Many redirect location: {}",
                    endpoint
                )));
            }

            let mut request = client.post(&endpoint).json(&body);
            if let Some(headers) = &self.config.headers {
                if !headers.is_empty() {
                    for (name, value) in headers {
                        request = request.header(name, value);
                    }
                }
            }

            let raw_response = request.send().await?;
            if let Some(location) = raw_response.headers().get(reqwest::header::LOCATION) {
                let redirect_url = location.to_str().map_err(|e| {
                    GraphQLError::with_text(format!(
                        "Failed to parse response header: Location. {:?}",
                        e
                    ))
                })?;

                // if the response location start with http:// or https://
                if redirect_url.starts_with("http://") || redirect_url.starts_with("https://") {
                    times += 1;
                    endpoint = redirect_url.to_string();
                    continue;
                }

                // without schema
                endpoint = if redirect_url.starts_with('/') {
                    format!("{}://{}{}", schema, host, redirect_url)
                } else {
                    format!("{}://{}/{}", schema, host, redirect_url)
                };
                times += 1;
                continue;
            }

            let status = raw_response.status();
            let response_body_text = raw_response
                .text()
                .await
                .map_err(|e| GraphQLError::with_text(format!("Can not get response: {:?}", e)))?;

            let json: GraphQLResponse<K> =
                serde_json::from_str(&response_body_text).map_err(|e| {
                    GraphQLError::with_text(format!(
                        "Failed to parse response: {:?}. The response body is: {}",
                        e, response_body_text
                    ))
                })?;

            if !status.is_success() {
                return Err(GraphQLError::with_message_and_json(
                    format!("The response is [{}]", status.as_u16()),
                    json.errors.unwrap_or_default(),
                ));
            }

            // Check if error messages have been received
            if json.errors.is_some() {
                return Err(GraphQLError::with_json(json.errors.unwrap_or_default()));
            }
            if json.data.is_none() {
                tracing::warn!(
                    target = "gql-client",
                    response_text = response_body_text,
                    "The deserialized data is none, the response",
                );
            }

            return Ok(json.data);
        }
    }
}
