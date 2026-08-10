#![deny(warnings)]
#![deny(unsafe_code)]
//! Private, engine-packaged orchestration for the Dagger Rust SDK.
//!
//! This crate owns the typed boundary between the engine adapter and Rust code
//! generation. Canonical models reject ambiguous paths, mutable dependency sources,
//! malformed identities, and unknown wire fields before later slices add filesystem,
//! process, or Dagger I/O.

pub mod canonical;
pub mod cli;
pub mod descriptor;
pub mod diagnostic;
pub mod initialization;
pub mod model;
pub mod post_work;
pub mod project;
pub mod publication;
pub mod root;
pub mod runner;
pub mod scalar;

pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
pub use model::*;
pub use root::OperationRoot;
pub use scalar::*;
