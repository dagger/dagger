use serde::Deserialize;

#[derive(Clone, Debug, Deserialize, PartialEq)]
pub struct ConnectParams {
    pub port: u64,
    pub session_token: String,
}

impl ConnectParams {
    pub fn new(port: u64, session_token: &str) -> Self {
        Self {
            port,
            session_token: session_token.to_string(),
        }
    }

    pub fn url(&self) -> String {
        format!("http://127.0.0.1:{}/query", self.port)
    }
}
