use crate::{
    cli_session::CliSession, config::Config, connect_params::ConnectParams, downloader::Downloader,
};

pub struct Engine {}

impl Engine {
    pub fn new() -> Self {
        Self {}
    }

    fn from_cli(&self, cfg: &Config) -> eyre::Result<ConnectParams> {
        let cli = Downloader::new("0.3.10".into())?.get_cli()?;

        let cli_session = CliSession::new();

        Ok(cli_session.connect(cfg, &cli)?)
    }

    pub fn start(&self, cfg: &Config) -> eyre::Result<ConnectParams> {
        // TODO: Add from existing session as well
        self.from_cli(cfg)
    }
}

#[cfg(test)]
mod tests {
    use crate::{config::Config, connect_params::ConnectParams};

    use super::Engine;

    // TODO: these tests potentially have a race condition
    #[test]
    fn engine_can_start() {
        let engine = Engine::new();
        let params = engine.start(&Config::new(None, None, None, None)).unwrap();

        assert_ne!(
            params,
            ConnectParams {
                port: 123,
                session_token: "123".into()
            }
        )
    }
}
