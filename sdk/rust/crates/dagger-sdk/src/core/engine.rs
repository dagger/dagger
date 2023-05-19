use crate::core::DAGGER_ENGINE_VERSION;
use crate::core::{
    cli_session::CliSession, config::Config, connect_params::ConnectParams, downloader::Downloader,
};

pub struct Engine {}

impl Engine {
    pub fn new() -> Self {
        Self {}
    }

    async fn from_cli(&self, cfg: &Config) -> eyre::Result<(ConnectParams, tokio::process::Child)> {
        let cli = Downloader::new(DAGGER_ENGINE_VERSION.into())?
            .get_cli()
            .await?;

        let cli_session = CliSession::new();

        Ok(cli_session.connect(cfg, &cli).await?)
    }

    pub async fn start(
        &self,
        cfg: &Config,
    ) -> eyre::Result<(ConnectParams, Option<tokio::process::Child>)> {
        tracing::info!("starting dagger-engine");

        if let Ok(conn) = self.from_session_env().await {
            return Ok((conn, None));
        }

        let (conn, proc) = self.from_cli(cfg).await?;

        Ok((conn, Some(proc)))
    }

    async fn from_session_env(&self) -> eyre::Result<ConnectParams> {
        let port = std::env::var("DAGGER_SESSION_PORT").map(|p| p.parse::<u64>())??;
        let token = std::env::var("DAGGER_SESSION_TOKEN")?;

        Ok(ConnectParams {
            port,
            session_token: token,
        })
    }
}
