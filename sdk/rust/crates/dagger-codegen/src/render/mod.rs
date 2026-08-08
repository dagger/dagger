//! Rust syntax rendering from validated projection artifacts.
//!
//! Rendering accepts data only. Filesystem publication and process orchestration belong
//! to `dagger-bootstrap`, keeping this crate deterministic and embeddable.

use proc_macro2::TokenStream;

use crate::diagnostic::{CodegenError, DiagnosticKind};

/// Parses emitted tokens as a Rust source file before they cross the crate boundary.
pub(crate) fn validate_file(tokens: TokenStream) -> Result<syn::File, CodegenError> {
    syn::parse2(tokens)
        .map_err(|error| CodegenError::new(DiagnosticKind::Render, error.to_string()))
}
