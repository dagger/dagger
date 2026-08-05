//! Deterministic completeness contract for the Dagger Rust SDK.
//!
//! This crate is deliberately independent of the public Rust SDK crates it assesses.

pub mod canonical;
pub mod diagnostic;
pub mod model;
pub mod target;

pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, ToolError, Validation,
};
pub use model::*;
pub use target::{
    GoVersionLabelResolution, TargetObservation, ValidatedTargetDescriptor, validate_target,
};
