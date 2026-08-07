use std::sync::Arc;

use dagger_sdk::{Diagnostic, DiagnosticSink, DiagnosticSinkError};

struct BorrowedSink<'a>(&'a str);

impl DiagnosticSink for BorrowedSink<'_> {
    fn emit(&self, _diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
        let _ = self.0;
        Ok(())
    }
}

fn main() {
    let label = String::from("short lived");
    let _ = dagger_sdk::ClientConfig::builder()
        .diagnostic_sink(Arc::new(BorrowedSink(&label)))
        .build();
}
