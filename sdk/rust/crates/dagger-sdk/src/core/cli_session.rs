use std::{fs::canonicalize, path::PathBuf, process::Stdio, sync::Arc};

use tokio::io::AsyncBufReadExt;

use crate::core::{config::Config, connect_params::ConnectParams};

#[derive(Clone, Debug)]
pub struct CliSession {
    inner: Arc<InnerCliSession>,
}

impl CliSession {
    pub fn new() -> Self {
        Self {
            inner: Arc::new(InnerCliSession {}),
        }
    }

    pub async fn connect(
        &self,
        config: &Config,
        cli_path: &PathBuf,
    ) -> eyre::Result<(ConnectParams, tokio::process::Child)> {
        self.inner.connect(config, cli_path).await
    }
}

#[derive(Debug)]
struct InnerCliSession {}

impl InnerCliSession {
    pub async fn connect(
        &self,
        config: &Config,
        cli_path: &PathBuf,
    ) -> eyre::Result<(ConnectParams, tokio::process::Child)> {
        let proc = self.start(config, cli_path)?;
        let params = self.get_conn(proc, config).await?;
        Ok(params)
    }

    fn start(&self, config: &Config, cli_path: &PathBuf) -> eyre::Result<tokio::process::Child> {
        let mut args: Vec<String> = vec!["session".into()];
        if let Some(workspace) = &config.workdir_path {
            let abs_path = canonicalize(workspace)?;
            args.extend(["--workdir".into(), abs_path.to_string_lossy().to_string()])
        }
        if let Some(config_path) = &config.config_path {
            let abs_path = canonicalize(config_path)?;
            args.extend(["--project".into(), abs_path.to_string_lossy().to_string()])
        }

        args.extend(["--label".into(), "dagger.io/sdk.name:rust".into()]);
        args.extend([
            "--label".into(),
            format!("dagger.io/sdk.version:{}", env!("CARGO_PKG_VERSION")).into(),
        ]);

        let proc = tokio::process::Command::new(
            cli_path
                .to_str()
                .ok_or(eyre::anyhow!("could not get string from path"))?,
        )
        .args(args.as_slice())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()?;

        //TODO: Add retry mechanism

        return Ok(proc);
    }

    async fn get_conn(
        &self,
        mut proc: tokio::process::Child,
        config: &Config,
    ) -> eyre::Result<(ConnectParams, tokio::process::Child)> {
        let stdout = proc
            .stdout
            .take()
            .ok_or(eyre::anyhow!("could not acquire stdout from child process"))?;

        let stderr = proc
            .stderr
            .take()
            .ok_or(eyre::anyhow!("could not acquire stderr from child process"))?;

        let (sender, mut receiver) = tokio::sync::mpsc::channel(1);

        let logger = config.logger.as_ref().map(|p| p.clone());
        tokio::spawn(async move {
            let mut stdout_bufr = tokio::io::BufReader::new(stdout).lines();
            while let Ok(Some(line)) = stdout_bufr.next_line().await {
                if let Ok(conn) = serde_json::from_str::<ConnectParams>(&line) {
                    sender.send(conn).await.unwrap();
                    continue;
                }

                if let Some(logger) = &logger {
                    logger.stdout(&line).unwrap();
                }
            }
        });

        let logger = config.logger.as_ref().map(|p| p.clone());
        tokio::spawn(async move {
            let mut stderr_bufr = tokio::io::BufReader::new(stderr).lines();
            while let Ok(Some(line)) = stderr_bufr.next_line().await {
                if let Some(logger) = &logger {
                    logger.stdout(&line).unwrap();
                }
            }
        });

        let conn = receiver.recv().await.ok_or(eyre::anyhow!(
            "could not receive ok signal from dagger-engine"
        ))?;

        Ok((conn, proc))
    }
}
