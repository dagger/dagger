//! Legacy CLI download and platform-selection implementation.
//!
//! The stable provisioner owns the release-grade verified path; this module remains
//! private until all beta connection entry points have been retired.

use std::{
    fs::File,
    io::{Write, copy},
    os::unix::prelude::PermissionsExt,
    path::{Path, PathBuf},
};

use eyre::Context;
use flate2::read::GzDecoder;
use platform_info::{PlatformInfoAPI, UNameAPI};
use reqwest::StatusCode;
use sha2::Digest;
use tar::Archive;
use tempfile::tempfile;
use thiserror::Error;

use crate::errors::DaggerError;

#[allow(dead_code)]
#[derive(Clone)]
pub struct Platform {
    pub os: String,
    pub arch: String,
}

impl Platform {
    pub fn from_system() -> Platform {
        let platform = platform_info::PlatformInfo::new()
            .expect("Unable to determine platform information, use `dagger run <app> instead`");
        let os_name = platform.sysname().to_string_lossy().to_lowercase();
        let arch = platform.machine().to_string_lossy().to_lowercase();
        let normalize_arch = match arch.as_str() {
            "x86_64" => "amd64",
            "aarch" => "arm64",
            "aarch64" => "arm64",
            arch => arch,
        };

        Self {
            os: os_name,
            arch: normalize_arch.into(),
        }
    }
}

#[allow(dead_code)]
pub struct TempFile {
    prefix: String,
    directory: PathBuf,
    file: File,
}

#[allow(dead_code)]
impl TempFile {
    pub(crate) fn new(prefix: &str, directory: &Path) -> eyre::Result<Self> {
        let prefix = prefix.to_string();

        let file = tempfile()?;

        Ok(Self {
            prefix,
            file,
            directory: directory.to_path_buf(),
        })
    }
}

#[allow(dead_code)]
pub type CliVersion = String;

#[allow(dead_code)]
pub struct Downloader {
    version: CliVersion,
    platform: Platform,
    cli_base_url: String,
}
#[allow(dead_code)]
const DEFAULT_CLI_HOST: &str = "dl.dagger.io";
#[allow(dead_code)]
const CLI_BIN_PREFIX: &str = "dagger-";
#[allow(dead_code)]
const CLI_BASE_URL: &str = "https://dl.dagger.io/dagger/releases";

#[allow(dead_code)]
impl Downloader {
    pub fn new(version: CliVersion) -> Self {
        Self {
            version,
            platform: Platform::from_system(),
            cli_base_url: CLI_BASE_URL.into(),
        }
    }

    pub fn archive_url(&self) -> String {
        let ext = match self.platform.os.as_str() {
            "windows" => "zip",
            _ => "tar.gz",
        };
        let version = &self.version;
        let os = &self.platform.os;
        let arch = &self.platform.arch;

        format!(
            "{}/{version}/dagger_v{version}_{os}_{arch}.{ext}",
            self.cli_base_url
        )
    }

    pub fn checksum_url(&self) -> String {
        let version = &self.version;

        format!("{}/{version}/checksums.txt", self.cli_base_url)
    }

    pub(crate) fn cache_dir(&self) -> eyre::Result<PathBuf> {
        let env = std::env::var("XDG_CACHE_HOME").unwrap_or("".into());
        let env = env.trim();
        let mut path = match env {
            "" => dirs::cache_dir().ok_or(eyre::anyhow!(
                "could not find cache_dir, either in env or XDG_CACHE_HOME"
            ))?,
            path => PathBuf::from(path),
        };

        path.push("dagger");

        std::fs::create_dir_all(&path)?;

        Ok(path)
    }

    pub async fn get_cli(&self) -> Result<PathBuf, DaggerError> {
        let version = &self.version;
        let mut cli_bin_path = self
            .cache_dir()
            .map_err(DaggerError::from_legacy_download)?;
        cli_bin_path.push(format!("{CLI_BIN_PREFIX}{version}"));
        if self.platform.os == "windows" {
            cli_bin_path = cli_bin_path.with_extension("exe")
        }

        if !cli_bin_path.exists() {
            cli_bin_path = self
                .download(cli_bin_path)
                .await
                .context("failed to download CLI from archive")
                .map_err(DaggerError::from_legacy_download)?;
        }

        Ok(cli_bin_path)
    }

    async fn download(&self, path: PathBuf) -> eyre::Result<PathBuf> {
        let expected_checksum = self.expected_checksum().await?;

        let mut bytes = vec![];
        let actual_hash = self.extract_cli_archive(&mut bytes).await?;

        if expected_checksum != actual_hash {
            eyre::bail!(
                "downloaded CLI binary checksum: {actual_hash} doesn't match checksum from checksums.txt: {expected_checksum}"
            )
        }

        let mut file = std::fs::File::create(&path)?;
        let meta = file.metadata()?;
        let mut perm = meta.permissions();
        perm.set_mode(0o700);
        file.set_permissions(perm)?;
        file.write_all(bytes.as_slice())?;

        Ok(path)
    }

    async fn expected_checksum(&self) -> eyre::Result<String> {
        let archive_url = &self.archive_url();
        let archive_path = PathBuf::from(&archive_url);
        let archive_name = archive_path
            .file_name()
            .ok_or(eyre::anyhow!("could not get file_name from archive_url"))?;
        let checksum_url = self.checksum_url();
        let resp = reqwest::get(&checksum_url).await?;
        let status = resp.status();
        if is_cli_release_unavailable(status) {
            return Err(CliReleaseUnavailableError {
                url: checksum_url,
                status,
            }
            .into());
        }
        let resp = resp.error_for_status()?;
        for line in resp.text().await?.lines() {
            let mut content = line.split_whitespace();
            let checksum = content
                .next()
                .ok_or(eyre::anyhow!("could not find checksum in checksums.txt"))?;
            let file_name = content
                .next()
                .ok_or(eyre::anyhow!("could not find file_name in checksums.txt"))?;

            if file_name == archive_name {
                return Ok(checksum.to_string());
            }
        }

        eyre::bail!("could not find a matching version or binary in checksums.txt")
    }

    pub(crate) async fn extract_cli_archive(&self, dest: &mut Vec<u8>) -> eyre::Result<String> {
        let archive_url = self.archive_url();
        let resp = reqwest::get(&archive_url).await?;
        let resp = resp.error_for_status()?;
        let bytes = resp.bytes().await?;
        let mut hasher = sha2::Sha256::new();
        hasher.update(&bytes);
        let res = hasher.finalize();

        if archive_url.ends_with(".zip") {
            // TODO:  Nothing for now
            todo!()
        } else {
            self.extract_from_tar(&bytes, dest)?;
        }

        Ok(hex::encode(res))
    }

    fn extract_from_tar(&self, temp: &[u8], output: &mut Vec<u8>) -> eyre::Result<()> {
        let decompressed_temp = GzDecoder::new(temp);
        let mut archive = Archive::new(decompressed_temp);

        for entry in archive.entries()? {
            let mut entry = entry?;
            let path = entry.path()?;

            if path.ends_with("dagger") {
                copy(&mut entry, output)?;

                return Ok(());
            }
        }

        eyre::bail!("could not find a matching file")
    }
}

#[derive(Debug, Error)]
#[error("CLI release unavailable: failed to download checksums from {url}: {status}")]
pub(super) struct CliReleaseUnavailableError {
    pub(super) url: String,
    pub(super) status: StatusCode,
}

pub(super) fn has_cli_release_unavailable_error(error: &DaggerError) -> bool {
    let mut current: Option<&(dyn std::error::Error + 'static)> = Some(error);
    while let Some(source) = current {
        if source
            .downcast_ref::<CliReleaseUnavailableError>()
            .is_some()
        {
            return true;
        }
        current = source.source();
    }
    false
}

fn is_cli_release_unavailable(status: StatusCode) -> bool {
    // dl.dagger.io returns 403 for missing S3 objects.
    status == StatusCode::FORBIDDEN || status == StatusCode::NOT_FOUND
}

#[cfg(test)]
mod tests {
    use reqwest::StatusCode;
    use tokio::{
        io::{AsyncReadExt, AsyncWriteExt},
        net::TcpListener,
    };

    use crate::errors::DaggerError;

    use super::{
        CliReleaseUnavailableError, Downloader, Platform, has_cli_release_unavailable_error,
    };

    #[tokio::test]
    async fn download() {
        let cli_path = Downloader::new("0.3.10".into()).get_cli().await.unwrap();

        assert_eq!(
            Some("dagger-0.3.10"),
            cli_path.file_name().and_then(|s| s.to_str())
        )
    }

    #[tokio::test]
    async fn checksum_marks_release_unavailable() {
        for status in [StatusCode::FORBIDDEN, StatusCode::NOT_FOUND] {
            let downloader = test_downloader(status_server(status).await);

            let error = downloader.expected_checksum().await.unwrap_err();

            assert!(error.downcast_ref::<CliReleaseUnavailableError>().is_some());
        }
    }

    #[tokio::test]
    async fn missing_archive_does_not_mark_release_unavailable() {
        // Missing checksums mean the release is absent; a missing archive may be a
        // partial or broken release, so it must remain fatal.
        for status in [StatusCode::FORBIDDEN, StatusCode::NOT_FOUND] {
            let downloader = test_downloader(status_server(status).await);

            let error = downloader
                .extract_cli_archive(&mut Vec::new())
                .await
                .unwrap_err();

            assert!(error.downcast_ref::<CliReleaseUnavailableError>().is_none());
        }
    }

    #[test]
    fn detects_release_unavailable_through_download_context() {
        let error = CliReleaseUnavailableError {
            url: "https://example.test/checksums.txt".into(),
            status: StatusCode::NOT_FOUND,
        };
        let error = eyre::Report::new(error).wrap_err("failed to download CLI from archive");
        let error = DaggerError::from_legacy_download(error);

        assert!(has_cli_release_unavailable_error(&error));
    }

    fn test_downloader(cli_base_url: String) -> Downloader {
        Downloader {
            version: "unreleased".into(),
            platform: Platform {
                os: "linux".into(),
                arch: "amd64".into(),
            },
            cli_base_url,
        }
    }

    async fn status_server(status: StatusCode) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();

        tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = [0; 1024];
            let bytes_read = stream.read(&mut request).await.unwrap();
            assert!(bytes_read > 0, "the client must send an HTTP request");
            let response = format!(
                "HTTP/1.1 {} {}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
                status.as_u16(),
                status.canonical_reason().unwrap()
            );
            stream.write_all(response.as_bytes()).await.unwrap();
        });

        format!("http://{address}")
    }
}
