use std::{
    fs::canonicalize,
    io::{BufRead, BufReader},
    path::PathBuf,
    process::{Child, Stdio},
    sync::Arc,
};

use crate::{config::Config, connect_params::ConnectParams};

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
    ) -> eyre::Result<(ConnectParams, Child)> {
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
    ) -> eyre::Result<(ConnectParams, Child)> {
        let proc = self.start(config, cli_path)?;
        let params = self.get_conn(proc).await?;
        Ok(params)
    }

    fn start(&self, config: &Config, cli_path: &PathBuf) -> eyre::Result<std::process::Child> {
        let mut args: Vec<String> = vec!["session".into()];
        if let Some(workspace) = &config.workdir_path {
            let abs_path = canonicalize(workspace)?;
            args.extend(["--workdir".into(), abs_path.to_string_lossy().to_string()])
        }
        if let Some(config_path) = &config.config_path {
            let abs_path = canonicalize(config_path)?;
            args.extend(["--project".into(), abs_path.to_string_lossy().to_string()])
        }

        let proc = std::process::Command::new(
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
        mut proc: std::process::Child,
    ) -> eyre::Result<(ConnectParams, std::process::Child)> {
        let stdout = proc
            .stdout
            .take()
            .ok_or(eyre::anyhow!("could not acquire stdout from child process"))?;

        let stderr = proc
            .stderr
            .take()
            .ok_or(eyre::anyhow!("could not acquire stderr from child process"))?;

        let (sender, mut receiver) = tokio::sync::mpsc::channel(1);

        tokio::spawn(async move {
            let stdout_bufr = BufReader::new(stdout);
            for line in stdout_bufr.lines() {
                let out = line.as_ref().unwrap();
                if let Ok(conn) = serde_json::from_str::<ConnectParams>(&out) {
                    sender.send(conn).await.unwrap();
                }
                if let Ok(line) = line {
                    println!("dagger: {}", line);
                }
            }
        });

        tokio::spawn(async move {
            let stderr_bufr = BufReader::new(stderr);
            for line in stderr_bufr.lines() {
                if let Ok(line) = line {
                    println!("dagger: {}", line);
                }
                //panic!("could not start dagger session: {}", out)
            }
        });

        let conn = receiver.recv().await.ok_or(eyre::anyhow!("could not receive ok signal from dagger-engine"))?;

        Ok((conn, proc))
    }
}
