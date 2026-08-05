//! Deterministic completeness contract for the Dagger Rust SDK.
//!
//! This crate is deliberately independent of the public Rust SDK crates it assesses.

pub mod authority;
pub mod canonical;
pub mod diagnostic;
pub mod extract;
pub mod inventory;
pub mod io;
pub mod model;
pub mod target;

pub use authority::{
    AuthoritySourceBundles, SourceBundle, SourceCoverage, SourceItemCoverage,
    SourceItemDisposition, ValidatedAuthorityRegistry, ValidatedAuthoritySources,
    ValidatedSourceCoverage, recompute_source_digest, validate_authority_registry,
    validate_authority_sources, validate_source_coverage,
};
pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, ToolError, Validation,
};
pub use inventory::{
    CapabilityCandidate, CapabilityOrigin, behavior_capability_id, build_inventory,
    decode_identity_segment, derive_schema_candidates, encode_identity_segment,
    policy_capability_id, schema_capability_id, semantic_fingerprint,
};
pub use io::{ImmutableTransitionRetrieval, RepositoryRoots, SourceLoadError, load_source_bundles};
pub use model::*;
pub use target::{
    GoVersionLabelResolution, TargetObservation, ValidatedTargetDescriptor, validate_target,
};
