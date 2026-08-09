//! Compatibility loggers for legacy connector output.
//!
//! Stable request diagnostics use [`crate::DiagnosticSink`]; these adapters remain
//! private so their stdout/stderr behaviour cannot become a public compatibility seam.

use crate::{
    core::logger::{DynLogger, Logger},
    diagnostic::DiagnosticSinkError,
};
use tracing::Level;

pub fn default_logging() -> Result<(), DiagnosticSinkError> {
    tracing_subscriber::fmt()
        .with_max_level(Level::INFO)
        .try_init()
        .map_err(DiagnosticSinkError::with_boxed_source)
}

#[derive(Default)]
pub struct StdLogger {}

impl Logger for StdLogger {
    fn stdout(&self, output: &str) -> Result<(), DiagnosticSinkError> {
        println!("{}", output);

        Ok(())
    }

    fn stderr(&self, output: &str) -> Result<(), DiagnosticSinkError> {
        eprintln!("{}", output);

        Ok(())
    }
}

#[derive(Default)]
pub struct TracingLogger {}

impl Logger for TracingLogger {
    fn stdout(&self, output: &str) -> Result<(), DiagnosticSinkError> {
        tracing::info!(output = output, "dagger-sdk");

        Ok(())
    }

    fn stderr(&self, output: &str) -> Result<(), DiagnosticSinkError> {
        tracing::warn!(output = output, "dagger-sdk");

        Ok(())
    }
}

#[derive(Default)]
pub struct AggregateLogger {
    pub loggers: Vec<DynLogger>,
}

impl Logger for AggregateLogger {
    fn stdout(&self, output: &str) -> Result<(), DiagnosticSinkError> {
        for logger in &self.loggers {
            logger.stdout(output)?;
        }

        Ok(())
    }

    fn stderr(&self, output: &str) -> Result<(), DiagnosticSinkError> {
        for logger in &self.loggers {
            logger.stderr(output)?;
        }

        Ok(())
    }
}
