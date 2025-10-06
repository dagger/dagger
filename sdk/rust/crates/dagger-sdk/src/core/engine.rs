use crate::core::DAGGER_ENGINE_VERSION;
use crate::core::{
    cli_session::CliSession, config::Config, connect_params::ConnectParams, downloader::Downloader,
};
use std::path::PathBuf;

use super::cli_session::DaggerSessionProc;

#[derive(Default)]
pub struct Engine {}

impl Engine {
    pub fn new() -> Self {
        Self {}
    }

    #[allow(clippy::wrong_self_convention)]
    async fn from_cli(&self, cfg: &Config) -> eyre::Result<(ConnectParams, DaggerSessionProc)> {
        let cli = Downloader::new(DAGGER_ENGINE_VERSION.into())
            .get_cli()
            .await?;

        let cli_session = CliSession::new();

        cli_session.connect(cfg, &cli).await
    }

    pub async fn start(
        &self,
        cfg: &Config,
    ) -> eyre::Result<(ConnectParams, Option<DaggerSessionProc>)> {
        tracing::info!("starting dagger-engine");

        if let Ok(conn) = self.from_session_env().await {
            return Ok((conn, None));
        }

        if let Ok((conn, child)) = self.from_local_cli(cfg).await {
            return Ok((conn, Some(child)));
        }

        let (conn, proc) = self.from_cli(cfg).await?;

        Ok((conn, Some(proc)))
    }

    #[allow(clippy::wrong_self_convention)]
    async fn from_session_env(&self) -> eyre::Result<ConnectParams> {
        let port = std::env::var("DAGGER_SESSION_PORT").map(|p| p.parse::<u64>())??;
        let token = std::env::var("DAGGER_SESSION_TOKEN")?;

        Ok(ConnectParams {
            port,
            session_token: token,
        })
    }

    #[allow(clippy::wrong_self_convention)]
    async fn from_local_cli(
        &self,
        cfg: &Config,
    ) -> eyre::Result<(ConnectParams, DaggerSessionProc)> {
        let bin: PathBuf = std::env::var("_EXPERIMENTAL_DAGGER_CLI_BIN")?.into();
        let cli_session = CliSession::new();

        cli_session.connect(cfg, &bin).await
    }
}
