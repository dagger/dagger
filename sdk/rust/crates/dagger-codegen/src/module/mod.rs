//! Pure Rust module-authoring models and source interpretation.
//!
//! This boundary accepts immutable typed values and returns typed values. It owns no
//! filesystem, process, network, Cargo, engine, publication, or user-code execution.

pub mod authoring;
pub mod canonical;
pub mod diagnostic;
pub mod model;

pub use authoring::{
    AuthoringDeclaration, AuthoringDeclarationKind, AuthoringField, AuthoringFieldPolicy,
    AuthoringFunction, AuthoringParameter, AuthoringParser, AuthoringVisibility,
};
pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use diagnostic::{
    ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet, SafeDiagnosticSource,
};
pub use model::*;
