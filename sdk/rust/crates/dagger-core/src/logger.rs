use std::sync::Arc;

pub trait Logger {
    fn stdout(&self, output: &str) -> eyre::Result<()>;
    fn stderr(&self, output: &str) -> eyre::Result<()>;
}

pub type DynLogger = Arc<dyn Logger + Send + Sync>;
