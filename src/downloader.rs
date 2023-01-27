use std::{
    fs::File,
    io::{BufWriter, Read, Write},
    path::PathBuf,
};

use eyre::Context;
use platform_info::Uname;
use tempfile::{tempfile, NamedTempFile};

#[derive(Clone)]
pub struct Platform {
    pub os: String,
    pub arch: String,
}

impl Platform {
    pub fn from_system() -> eyre::Result<Self> {
        let platform = platform_info::PlatformInfo::new()?;
        let os_name = platform.sysname();
        let arch = platform.machine().to_lowercase();
        let normalize_arch = match arch.as_str() {
            "x86_64" => "amd64",
            "aarch" => "arm64",
            arch => arch,
        };

        Ok(Self {
            os: os_name.to_lowercase(),
            arch: normalize_arch.into(),
        })
    }
}

pub struct TempFile {
    prefix: String,
    directory: PathBuf,
    file: File,
}

impl TempFile {
    pub fn new(prefix: &str, directory: &PathBuf) -> eyre::Result<Self> {
        let prefix = prefix.to_string();
        let directory = directory.clone();

        let file = tempfile()?;

        Ok(Self {
            prefix,
            directory,
            file,
        })
    }
}

pub type CliVersion = String;

pub struct Downloader {
    version: CliVersion,
    platform: Platform,
}
const DEFAULT_CLI_HOST: &str = "dl.dagger.io";
const CLI_BIN_PREFIX: &str = "dagger-";
const CLI_BASE_URL: &str = "https://dl.dagger.io/dagger/releases";

impl Downloader {
    pub fn new(version: CliVersion) -> eyre::Result<Self> {
        Ok(Self {
            version,
            platform: Platform::from_system()?,
        })
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

    pub fn get_cli(&self) -> eyre::Result<PathBuf> {
        let version = &self.version;
        let mut cli_bin_path = self.cache_dir()?;
        cli_bin_path.push(format!("{CLI_BIN_PREFIX}{version}"));
        if self.platform.os == "windows" {
            cli_bin_path = cli_bin_path.with_extension("exe")
        }

        if !cli_bin_path.exists() {
            cli_bin_path = self
                .download(cli_bin_path)
                .context("failed to download CLI from archive")?;
        }

        for file in self.cache_dir()?.read_dir()? {
            if let Ok(entry) = file {
                let path = entry.path();
                if path != cli_bin_path {
                    std::fs::remove_file(path)?;
                }
            }
        }

        Ok(cli_bin_path)
    }

    fn download(&self, _path: PathBuf) -> eyre::Result<PathBuf> {
        let expected_checksum = self.expected_checksum()?;

        let (actual_hash, _tempbin) = self.extract_cli_archive()?;

        if expected_checksum != actual_hash {
            eyre::bail!("downloaded CLI binary checksum doesn't match checksum from checksums.txt")
        }

        todo!();
    }

    fn expected_checksum(&self) -> eyre::Result<String> {
        let archive_url = &self.archive_url();
        let archive_path = PathBuf::from(&archive_url);
        let archive_name = archive_path
            .file_name()
            .ok_or(eyre::anyhow!("could not get file_name from archive_url"))?;
        let resp = reqwest::blocking::get(self.checksum_url())?;
        let resp = resp.error_for_status()?;
        for line in resp.text()?.lines() {
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

    pub fn extract_cli_archive(&self) -> eyre::Result<(String, File)> {
        let archive_url = self.archive_url();
        let resp = reqwest::blocking::get(&archive_url)?;
        let mut resp = resp.error_for_status()?;
        let temp = NamedTempFile::new()?;
        let (file, path) = temp.keep()?;
        println!("path: {:?}", path);

        let mut buf_writer = BufWriter::new(file);
        let mut bytes = vec![];
        let _ = resp.read_to_end(&mut bytes)?;
        buf_writer.write_all(bytes.as_slice())?;

        if archive_url.ends_with(".zip") {
            // TODO:  Nothing for now
        } else {
            //self.extract_from_tar(&temp)?;
        }
        todo!()
    }
}

#[cfg(test)]
mod test {
    use std::path::PathBuf;

    use super::Downloader;

    #[test]
    fn download() {
        let cli_path = Downloader::new("0.3.10".into()).unwrap().get_cli().unwrap();

        assert_eq!(PathBuf::from("/"), cli_path)
    }
}
