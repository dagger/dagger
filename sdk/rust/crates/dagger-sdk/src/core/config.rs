use crate::core::logger::DynLogger;
use derive_builder::Builder;
use std::path::PathBuf;

#[derive(Builder)]
#[builder(build_fn(private, name = "fallible_build"))]
#[builder(setter(strip_option))]
pub struct Config {
    #[builder(default = "None")]
    /// The host workdir loaded into dagger.
    pub workdir_path: Option<PathBuf>,
    #[builder(default = "None")]
    /// Project configuration file path.
    pub config_path: Option<PathBuf>,
    #[builder(default = "10000")]
    /// The maximum time in milliseconds for establishing a connection to the server.
    /// Defaults to 10 seconds.
    pub timeout_ms: u64,
    #[builder(default = "None")]
    /// The maximum time in milliseconds for executing a request.
    /// Defaults to no timeout.
    pub execute_timeout_ms: Option<u64>,
    #[builder(default = "None")]
    /// Logger implementation to handle logs from the engine.
    pub logger: Option<DynLogger>,
}

impl ConfigBuilder {
    pub fn build(&mut self) -> Config {
        self.fallible_build()
            .expect("all fields have default values")
    }
}

impl Default for Config {
    fn default() -> Self {
        Self {
            workdir_path: None,
            config_path: None,
            timeout_ms: 10 * 1000,
            execute_timeout_ms: None,
            logger: None,
        }
    }
}

impl Config {
    pub fn new(
        workdir_path: Option<PathBuf>,
        config_path: Option<PathBuf>,
        timeout_ms: Option<u64>,
        execute_timeout_ms: Option<u64>,
        logger: Option<DynLogger>,
    ) -> Self {
        Self {
            workdir_path,
            config_path,
            timeout_ms: timeout_ms.unwrap_or(10 * 1000),
            execute_timeout_ms,
            logger,
        }
    }

    /// Returns a new config builder instance
    pub fn builder() -> ConfigBuilder {
        ConfigBuilder::default()
    }
}

#[cfg(test)]
mod tests {
    use super::Config;

    #[test]
    fn default_timeout_is_10s() {
        let cfg = Config::default();
        assert_eq!(cfg.timeout_ms, 10 * 1000);
        assert!(cfg.execute_timeout_ms.is_none());
    }
}
