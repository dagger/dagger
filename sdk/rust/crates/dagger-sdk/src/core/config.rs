use std::path::PathBuf;

use crate::core::logger::DynLogger;

pub struct Config {
    pub workdir_path: Option<PathBuf>,
    pub config_path: Option<PathBuf>,
    pub timeout_ms: u64,
    pub execute_timeout_ms: Option<u64>,
    pub logger: Option<DynLogger>,
}

impl Default for Config {
    fn default() -> Self {
        Self::new(None, None, None, None, None)
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
}
