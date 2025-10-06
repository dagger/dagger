use std::{
    fs::File,
    io::{copy, Write},
    os::unix::prelude::PermissionsExt,
    path::{Path, PathBuf},
};

use eyre::Context;
use flate2::read::GzDecoder;
use platform_info::{PlatformInfoAPI, UNameAPI};
use sha2::Digest;
use tar::Archive;
use tempfile::tempfile;

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
    pub fn new(prefix: &str, directory: &Path) -> eyre::Result<Self> {
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

        format!("{CLI_BASE_URL}/{version}/dagger_v{version}_{os}_{arch}.{ext}")
    }

    pub fn checksum_url(&self) -> String {
        let version = &self.version;

        format!("{CLI_BASE_URL}/{version}/checksums.txt")
    }

    pub fn cache_dir(&self) -> eyre::Result<PathBuf> {
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
        let mut cli_bin_path = self.cache_dir().map_err(DaggerError::DownloadClient)?;
        cli_bin_path.push(format!("{CLI_BIN_PREFIX}{version}"));
        if self.platform.os == "windows" {
            cli_bin_path = cli_bin_path.with_extension("exe")
        }

        if !cli_bin_path.exists() {
            cli_bin_path = self
                .download(cli_bin_path)
                .await
                .context("failed to download CLI from archive")
                .map_err(DaggerError::DownloadClient)?;
        }

        Ok(cli_bin_path)
    }

    async fn download(&self, path: PathBuf) -> eyre::Result<PathBuf> {
        let expected_checksum = self.expected_checksum().await?;

        let mut bytes = vec![];
        let actual_hash = self.extract_cli_archive(&mut bytes).await?;

        if expected_checksum != actual_hash {
            eyre::bail!("downloaded CLI binary checksum: {actual_hash} doesn't match checksum from checksums.txt: {expected_checksum}")
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
        let resp = reqwest::get(self.checksum_url()).await?;
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

    pub async fn extract_cli_archive(&self, dest: &mut Vec<u8>) -> eyre::Result<String> {
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

#[cfg(test)]
mod test {
    use super::Downloader;

    #[tokio::test]
    async fn download() {
        let cli_path = Downloader::new("0.3.10".into()).get_cli().await.unwrap();

        assert_eq!(
            Some("dagger-0.3.10"),
            cli_path.file_name().and_then(|s| s.to_str())
        )
    }
}
