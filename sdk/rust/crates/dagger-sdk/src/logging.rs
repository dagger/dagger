use crate::core::logger::{DynLogger, Logger};
use tracing::Level;

pub fn default_logging() -> eyre::Result<()> {
    tracing_subscriber::fmt().with_max_level(Level::INFO).init();
    Ok(())
}

#[derive(Default)]
pub struct StdLogger {}

impl Logger for StdLogger {
    fn stdout(&self, output: &str) -> eyre::Result<()> {
        println!("{}", output);

        Ok(())
    }

    fn stderr(&self, output: &str) -> eyre::Result<()> {
        eprintln!("{}", output);

        Ok(())
    }
}

#[derive(Default)]
pub struct TracingLogger {}

impl Logger for TracingLogger {
    fn stdout(&self, output: &str) -> eyre::Result<()> {
        tracing::info!(output = output, "dagger-sdk");

        Ok(())
    }

    fn stderr(&self, output: &str) -> eyre::Result<()> {
        tracing::warn!(output = output, "dagger-sdk");

        Ok(())
    }
}

#[derive(Default)]
pub struct AggregateLogger {
    pub loggers: Vec<DynLogger>,
}

impl Logger for AggregateLogger {
    fn stdout(&self, output: &str) -> eyre::Result<()> {
        for logger in &self.loggers {
            logger.stdout(output).unwrap()
        }

        Ok(())
    }

    fn stderr(&self, output: &str) -> eyre::Result<()> {
        for logger in &self.loggers {
            logger.stderr(output).unwrap()
        }

        Ok(())
    }
}
