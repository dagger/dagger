//! Deterministic completeness contract for the Dagger Rust SDK.
//!
//! This crate is deliberately independent of the public Rust SDK crates it assesses.

pub mod authority;
pub mod canonical;
pub mod classification;
pub mod cli;
pub mod command;
pub mod compatibility;
pub mod contract;
pub mod core_codegen;
pub mod diagnostic;
pub mod evidence;
pub mod extract;
pub mod feature_scope;
pub mod harness;
pub mod inventory;
pub mod io;
pub mod model;
pub mod observation;
pub mod ownership;
pub mod report;
pub mod target;
pub mod traceability;
pub mod transition;

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
pub use cli::{ArtifactCliBackend, CliBackend, ContractCliBackend, run_with_backend};
pub use command::{CommandPolicy, command_defects};
pub use compatibility::{ValidatedCompatibilityClaim, validate_compatibility_claim};
pub use contract::{DerivedContract, derive_contract, rust_artifact_digest};
pub use core_codegen::{
    CoreCodegenScopeContract, CoreCodegenScopeTransition, apply_core_codegen_scope_correction,
    core_codegen_policy_contract,
};
pub use diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, ToolError, Validation,
};
pub use evidence::{
    EvidenceAuditContext, EvidenceEligibility, EvidenceSource, EvidenceSourceRegistry,
    ValidatedEvidenceRegistry, audit_evidence_registry,
};
pub use feature_scope::{
    FeatureContractPolicy, FeatureScopePolicy, ReviewedPolicyClause, client_lifecycle_contract,
    reviewed_feature_contracts, transport_contract,
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
pub use io::{
    ImmutableTransitionRetrieval, IsolatedStaging, RepositoryRoots, SourceLoadError,
    load_source_bundles,
};
pub use model::*;
pub use observation::{
    ExactTargetRun, TransportAssertion, TransportObservationKind, TransportObservationMode,
    TransportObservationRecord, TransportObservationRegistry, validate_transport_observations,
};
pub use ownership::{BlockingDomain, OwnershipAssignments, validate_blocking_ownership};
pub use report::{
    Gate, GateProfile, build_report, gate_exit_status, gate_passes, profile_gate,
    render_human_report,
};
pub use target::{
    GoVersionLabelResolution, TargetObservation, ValidatedTargetDescriptor, validate_target,
};
pub use traceability::{
    CandidateStatusChanges, ChildSpecDeclaration, FeatureScopeDeclaration, ResidualBlocker,
    parse_feature_scope_declaration, validate_downstream_traceability,
    validate_feature_scope_routing, validate_feature_status_changes,
    validate_ownership_only_correction,
};
pub use transition::{ContractSnapshot, diff_targets, drift_diagnostics};
