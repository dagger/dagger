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
pub mod packaging;
pub mod post_work;
pub mod project;
pub mod protocol;
pub mod publication;
pub mod root;
pub mod runner;
pub mod runtime;
pub mod scalar;
pub mod surface;

pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
pub use model::*;
pub use packaging::{
    DESCRIPTOR_PATH, PACKAGED_ASSET_MANIFEST_PATH, PackageIdentity, SecurityAuditGraph,
    SecuritySubject, SecuritySubjectKind, build_packaged_content, derive_shipped_audit_graph,
    validate_packaged_distribution, validate_packaged_source,
};
pub use root::OperationRoot;
pub use scalar::*;
