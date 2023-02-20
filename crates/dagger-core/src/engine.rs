

use crate::{
    cli_session::CliSession, config::Config, connect_params::ConnectParams, downloader::Downloader,
};

pub struct Engine {}

impl Engine {
    pub fn new() -> Self {
        Self {}
    }

    async fn from_cli(&self, cfg: &Config) -> eyre::Result<(ConnectParams, tokio::process::Child)> {
        let cli = Downloader::new("0.3.12".into())?.get_cli().await?;

        let cli_session = CliSession::new();

        Ok(cli_session.connect(cfg, &cli).await?)
    }

    pub async fn start(&self, cfg: &Config) -> eyre::Result<(ConnectParams, tokio::process::Child)> {
        // TODO: Add from existing session as well
        self.from_cli(cfg).await
    }
}

#[cfg(test)]
mod tests {
    use crate::{config::Config, connect_params::ConnectParams};

    use super::Engine;

    // TODO: these tests potentially have a race condition
    #[tokio::test]
    async fn engine_can_start() {
        let engine = Engine::new();
        let params = engine.start(&Config::new(None, None, None, None)).await.unwrap();

        assert_ne!(
            params.0,
            ConnectParams {
                port: 123,
                session_token: "123".into()
            }
        )
    }
}
