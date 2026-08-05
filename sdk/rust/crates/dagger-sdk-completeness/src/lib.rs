//! Deterministic completeness contract for the Dagger Rust SDK.
//!
//! This crate is deliberately independent of the public Rust SDK crates it assesses.

pub mod authority;
pub mod canonical;
pub mod classification;
pub mod command;
pub mod diagnostic;
pub mod evidence;
pub mod extract;
pub mod harness;
pub mod inventory;
pub mod io;
pub mod model;
pub mod ownership;
pub mod target;
pub mod traceability;

pub use authority::{
    AuthoritySourceBundles, SourceBundle, SourceCoverage, SourceItemCoverage,
    SourceItemDisposition, ValidatedAuthorityRegistry, ValidatedAuthoritySources,
    ValidatedSourceCoverage, recompute_source_digest, validate_authority_registry,
    validate_authority_sources, validate_source_coverage,
};
pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use classification::{resolve_classifications, validate_status_entries};
pub use command::{CommandPolicy, command_defects};
pub use diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, ToolError, Validation,
};
pub use evidence::{
    EvidenceAuditContext, EvidenceEligibility, EvidenceSource, EvidenceSourceRegistry,
    ValidatedEvidenceRegistry, audit_evidence_registry,
};
pub use harness::{
    HarnessAdmission, HarnessCheckInventory, HarnessCheckSource, HarnessCommandExecutor,
    HarnessMappingContext, HarnessProcessOutput, HarnessRunContext, ProcessHarnessExecutor,
    ValidatedHarnessMappings, admit_harness_result, build_harness_check_inventory,
    run_harness_check, validate_conformance_scenario, validate_harness_mappings,
};
pub use inventory::{
    CapabilityCandidate, CapabilityOrigin, behavior_capability_id, build_inventory,
    decode_identity_segment, derive_schema_candidates, encode_identity_segment,
    policy_capability_id, schema_capability_id, semantic_fingerprint,
};
pub use io::{ImmutableTransitionRetrieval, RepositoryRoots, SourceLoadError, load_source_bundles};
pub use model::*;
pub use ownership::{BlockingDomain, OwnershipAssignments, validate_blocking_ownership};
pub use target::{
    GoVersionLabelResolution, TargetObservation, ValidatedTargetDescriptor, validate_target,
};
pub use traceability::{
    CandidateStatusChanges, ChildSpecDeclaration, validate_downstream_traceability,
};
