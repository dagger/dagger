#![deny(warnings)]
#![deny(unsafe_code)]
//! Private, engine-packaged orchestration for the Dagger Rust SDK.
//!
//! This crate owns the typed boundary between the engine adapter and Rust code
//! generation. Canonical models reject ambiguous paths, mutable dependency sources,
//! malformed identities, and unknown wire fields before later slices add filesystem,
//! process, or Dagger I/O.

pub mod canonical;
pub mod model;
pub mod scalar;

pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use model::*;
pub use scalar::*;
