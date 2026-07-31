use crate::core::{
    cli_session::CliSession,
    config::Config,
    connect_params::ConnectParams,
    downloader::{has_cli_release_unavailable_error, Downloader},
};
use crate::core::{logger::DynLogger, DAGGER_ENGINE_VERSION};
use crate::errors::DaggerError;
use std::path::PathBuf;
use thiserror::Error;

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
            .await;

        self.connect_provisioned_cli(cfg, DAGGER_ENGINE_VERSION, cli)
            .await
    }

    async fn connect_provisioned_cli(
        &self,
        cfg: &Config,
        version: &str,
        cli: Result<PathBuf, DaggerError>,
    ) -> eyre::Result<(ConnectParams, DaggerSessionProc)> {
        let (cli, download_error) = match cli {
            Ok(cli) => (cli, None),
            Err(download_error) => {
                let (cli, download_error) =
                    fallback_to_local_cli(download_error, version, cfg.logger.as_ref())?;
                (cli, Some(download_error))
            }
        };
        let cli_session = CliSession::new();

        match cli_session.connect(cfg, &cli).await {
            Ok(result) => Ok(result),
            Err(fallback_error) => match download_error {
                Some(download_error) => Err(CliPathFallbackError {
                    download_error,
                    fallback_context: format!("failed to use CLI from PATH {cli:?}"),
                    fallback_error,
                }
                .into()),
                None => Err(fallback_error),
            },
        }
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

#[derive(Debug, Error)]
#[error("{download_error:#}\n{fallback_context}: {fallback_error:#}")]
struct CliPathFallbackError {
    download_error: eyre::Error,
    fallback_context: String,
    #[source]
    fallback_error: eyre::Error,
}

fn fallback_to_local_cli(
    download_error: DaggerError,
    version: &str,
    logger: Option<&DynLogger>,
) -> eyre::Result<(PathBuf, eyre::Error)> {
    if !has_cli_release_unavailable_error(&download_error) {
        return Err(download_error.into());
    }

    let download_error = eyre::Report::new(download_error);
    let bin_path = match which::which("dagger") {
        Ok(bin_path) => bin_path,
        Err(fallback_error) => {
            return Err(CliPathFallbackError {
                download_error,
                fallback_context: "dagger CLI not found in PATH".into(),
                fallback_error: fallback_error.into(),
            }
            .into())
        }
    };

    let warning = format!(
        "CLI version {version} is unavailable; using {} from PATH (version compatibility is not guaranteed).",
        bin_path.display()
    );
    if let Some(logger) = logger {
        // A failed warning write shouldn't turn a usable fallback into an
        // error; go/python/typescript ignore it too.
        let _ = logger.stderr(&warning);
    } else {
        eprintln!("{warning}");
    }

    Ok((bin_path, download_error))
}

#[cfg(test)]
mod tests {
    use std::{
        ffi::OsString,
        fs::File,
        io::Write,
        path::{Path, PathBuf},
        sync::{Arc, Mutex, MutexGuard},
    };

    #[cfg(unix)]
    use std::os::unix::fs::PermissionsExt;

    use eyre::eyre;
    use reqwest::StatusCode;
    use tempfile::TempDir;

    use crate::{
        core::{
            config::Config,
            downloader::CliReleaseUnavailableError,
            logger::{DynLogger, Logger},
        },
        errors::DaggerError,
    };

    use super::{fallback_to_local_cli, CliPathFallbackError, Engine};

    static PATH_LOCK: Mutex<()> = Mutex::new(());

    #[test]
    fn fallback_to_local_cli_uses_dagger_in_path() {
        let temp_dir = TempDir::new().unwrap();
        let bin_path = create_dagger_executable(&temp_dir);
        let _path_lock = path_lock();
        let _path = PathGuard::set(temp_dir.path());
        let logger = Arc::new(TestLogger::default());
        let dyn_logger: DynLogger = logger.clone();

        let (actual, _) = fallback_to_local_cli(
            unavailable_download_error(),
            "unreleased",
            Some(&dyn_logger),
        )
        .unwrap();

        assert_eq!(actual, bin_path);
        assert_eq!(
            logger.stderr.lock().unwrap().as_str(),
            format!(
                "CLI version unreleased is unavailable; using {} from PATH (version compatibility is not guaranteed).",
                bin_path.display()
            )
        );
    }

    #[test]
    fn no_fallback_to_local_cli_for_other_errors() {
        let download_error = DaggerError::DownloadClient(eyre!("download failed"));

        let error = fallback_to_local_cli(download_error, "unreleased", None).unwrap_err();

        assert!(error.downcast_ref::<DaggerError>().is_some());
        assert!(format!("{error:#}").contains("download failed"));
    }

    #[test]
    fn fallback_preserves_download_and_path_errors() {
        let temp_dir = TempDir::new().unwrap();
        let _path_lock = path_lock();
        let _path = PathGuard::set(temp_dir.path());

        let error =
            fallback_to_local_cli(unavailable_download_error(), "unreleased", None).unwrap_err();

        let fallback_error = error.downcast_ref::<CliPathFallbackError>().unwrap();
        assert!(fallback_error
            .download_error
            .downcast_ref::<DaggerError>()
            .is_some());
        assert!(fallback_error
            .fallback_error
            .downcast_ref::<which::Error>()
            .is_some());
        let rendered = format!("{error}");
        assert!(rendered.contains("CLI release unavailable"));
        assert!(rendered.contains("https://example.test/checksums.txt"));
        assert!(rendered.contains("dagger CLI not found in PATH"));
    }

    #[tokio::test]
    async fn fallback_session_error_preserves_download_error() {
        let temp_dir = TempDir::new().unwrap();
        let bin_path = create_dagger_executable(&temp_dir);
        let _path_lock = path_lock();
        let _path = PathGuard::set(temp_dir.path());
        let logger: DynLogger = Arc::new(TestLogger::default());
        let cfg = Config::builder().logger(logger).build();

        let result = Engine::new()
            .connect_provisioned_cli(&cfg, "unreleased", Err(unavailable_download_error()))
            .await;
        let error = match result {
            Ok(_) => panic!("expected fallback session to fail"),
            Err(error) => error,
        };

        let fallback_error = error.downcast_ref::<CliPathFallbackError>().unwrap();
        assert!(fallback_error
            .download_error
            .downcast_ref::<DaggerError>()
            .is_some());
        assert!(fallback_error
            .fallback_error
            .downcast_ref::<which::Error>()
            .is_none());
        let rendered = format!("{error}");
        assert!(rendered.contains("CLI release unavailable"));
        assert!(rendered.contains("https://example.test/checksums.txt"));
        assert!(rendered.contains(&format!("failed to use CLI from PATH {bin_path:?}")));
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn fallback_session_exiting_without_params_errors_instead_of_hanging() {
        let temp_dir = TempDir::new().unwrap();
        let bin_path = temp_dir.path().join("dagger");
        let mut file = File::create(&bin_path).unwrap();
        file.write_all(b"#!/bin/sh\nexit 1\n").unwrap();
        let mut permissions = file.metadata().unwrap().permissions();
        permissions.set_mode(0o700);
        file.set_permissions(permissions).unwrap();
        drop(file);

        let _path_lock = path_lock();
        let _path = PathGuard::set(temp_dir.path());
        let logger: DynLogger = Arc::new(TestLogger::default());
        let cfg = Config::builder().logger(logger).build();

        let result = tokio::time::timeout(
            std::time::Duration::from_secs(30),
            Engine::new().connect_provisioned_cli(
                &cfg,
                "unreleased",
                Err(unavailable_download_error()),
            ),
        )
        .await
        .expect("a session exiting without connect params must error, not hang");

        let error = match result {
            Ok(_) => panic!("expected fallback session to fail"),
            Err(error) => error,
        };
        assert!(format!("{error:#}").contains("could not receive ok signal"));
    }

    fn unavailable_download_error() -> DaggerError {
        let error = CliReleaseUnavailableError {
            url: "https://example.test/checksums.txt".into(),
            status: StatusCode::NOT_FOUND,
        };
        let error = eyre::Report::new(error).wrap_err("failed to download CLI from archive");
        DaggerError::DownloadClient(error)
    }

    fn create_dagger_executable(temp_dir: &TempDir) -> PathBuf {
        let bin_name = if cfg!(windows) {
            "dagger.exe"
        } else {
            "dagger"
        };
        let bin_path = temp_dir.path().join(bin_name);
        let mut file = File::create(&bin_path).unwrap();
        file.write_all(b"#!/definitely/missing/dagger-test-interpreter\n")
            .unwrap();

        #[cfg(unix)]
        {
            let mut permissions = file.metadata().unwrap().permissions();
            permissions.set_mode(0o700);
            file.set_permissions(permissions).unwrap();
        }

        bin_path
    }

    fn path_lock() -> MutexGuard<'static, ()> {
        PATH_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    struct PathGuard(Option<OsString>);

    impl PathGuard {
        fn set(path: &Path) -> Self {
            let previous = std::env::var_os("PATH");
            std::env::set_var("PATH", path);
            Self(previous)
        }
    }

    impl Drop for PathGuard {
        fn drop(&mut self) {
            match &self.0 {
                Some(path) => std::env::set_var("PATH", path),
                None => std::env::remove_var("PATH"),
            }
        }
    }

    #[derive(Default)]
    struct TestLogger {
        stderr: Mutex<String>,
    }

    impl Logger for TestLogger {
        fn stdout(&self, _output: &str) -> eyre::Result<()> {
            Ok(())
        }

        fn stderr(&self, output: &str) -> eyre::Result<()> {
            self.stderr.lock().unwrap().push_str(output);
            Ok(())
        }
    }
}
