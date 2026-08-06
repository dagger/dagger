use std::sync::Arc;

use crate::diagnostic::DiagnosticSinkError;

pub trait Logger {
    fn stdout(&self, output: &str) -> Result<(), DiagnosticSinkError>;
    fn stderr(&self, output: &str) -> Result<(), DiagnosticSinkError>;
}

pub type DynLogger = Arc<dyn Logger + Send + Sync>;
