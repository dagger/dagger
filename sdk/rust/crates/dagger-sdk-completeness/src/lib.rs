//! Deterministic completeness contract for the Dagger Rust SDK.
//!
//! This crate is deliberately independent of the public Rust SDK crates it assesses.

pub mod authority;
pub mod canonical;
pub mod classification;
pub mod cli;
pub mod client_generation;
pub mod command;
pub mod compatibility;
pub mod contract;
pub mod core_codegen;
pub mod diagnostic;
pub mod engine_integration;
pub mod evidence;
pub mod extract;
pub mod feature_scope;
pub mod harness;
pub mod inventory;
pub mod io;
pub mod model;
pub mod module_authoring;
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
pub use client_generation::{
    ClientAuthority, ClientClosureGate, ClientClosureGateDisposition, ClientClosureGateObservation,
    ClientClosureGateOutcome, ClientDependencyScope, ClientEvidenceAdmission, ClientEvidenceDomain,
    ClientEvidenceObservation, ClientEvidenceOutcome, ClientEvidencePhase,
    ClientFeatureEndGatePlan, ClientGenerationClosureEvidence, ClientGenerationClosureObservation,
    ClientGenerationCompletenessReport, ClientGenerationDiagnostic, ClientGenerationDiagnosticCode,
    ClientGenerationDiagnosticSet, ClientGenerationEvidenceArtifact, ClientGenerationFormatVersion,
    ClientGenerationMapping, ClientGenerationReport, ClientGenerationScope,
    ClientGenerationScopeInput, ClientImplementationSubject, ClientOwnershipCorrection,
    ClientReportSection, ClientSignoffAdmission, ClientSignoffArtifact, ClientSignoffArtifactInput,
    ClientSignoffCase, ClientSignoffCaseObservation, ClientSignoffCaseOutcome,
    ClientSignoffCaseSpec, ClientSignoffExecutionCounts, ClientSignoffInventory,
    ClientSignoffObservation, ClientSignoffPhaseTimings, ClientSignoffRun, ClientTerminalStatus,
    PreservedClientBoundary, admit_client_evidence, admit_client_generation_closure,
    apply_client_ownership_correction, build_client_signoff_artifact,
    build_client_signoff_inventory, client_generation_scope_input,
    client_implementation_closure_claims, client_signoff_verdict_digest,
    derive_client_generation_report, derive_client_generation_scope, plan_client_feature_end_gate,
    required_client_closure_gates, required_client_signoff_cases,
    validate_client_signoff_candidate,
};
pub use command::{CommandPolicy, command_defects};
pub use compatibility::{ValidatedCompatibilityClaim, validate_compatibility_claim};
pub use contract::{DerivedContract, derive_contract, rust_artifact_digest};
pub use core_codegen::{
    BindingRecord, ConformanceCategory, ConformanceObservation, CoreCodegenEvidenceClosure,
    CoreCodegenEvidencePolicy, CoreCodegenEvidenceRecord, CoreCodegenEvidenceRegistry,
    CoreCodegenEvidenceResult, CoreCodegenMappings, CoreCodegenScopeContract,
    CoreCodegenScopeTransition, CoreConformanceRun, EvidenceDomain, GeneratedArtifactKind,
    GeneratedArtifactProvenance, GeneratedArtifactRecord, GeneratedBindingManifest,
    ManifestBindingKind, MappingDisposition, ReviewedMappingRule, admit_core_codegen_evidence,
    apply_core_codegen_scope_correction, assemble_core_codegen_manifest,
    core_codegen_policy_contract, core_conformance_evidence, required_conformance_categories,
    validate_core_codegen_bijection, validate_core_codegen_manifest, verify_core_codegen_evidence,
};
pub use diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, ToolError, Validation,
};
pub use engine_integration::{
    AllowedTerminalStatus, CapabilityMapping, CaseId, CaseObservation, DelegatedContentDomain,
    EngineEvidenceDomain, EngineIntegrationEvidenceArtifact, EngineIntegrationEvidenceClosure,
    EngineIntegrationManifest, EngineIntegrationMappings, EngineIntegrationObservation,
    EngineIntegrationTransition, EngineMappingDisposition, ImplementationSubject,
    ValidatedEngineIntegrationMappings, apply_engine_integration_statuses,
    assemble_engine_integration_manifest, derive_engine_integration_status_changes,
    validate_engine_integration_mappings, verify_engine_integration_evidence,
};
pub use evidence::{
    EvidenceAuditContext, EvidenceEligibility, EvidenceSource, EvidenceSourceRegistry,
    ValidatedEvidenceRegistry, audit_evidence_registry,
};
pub use feature_scope::{
    FeatureContractPolicy, FeatureScopePolicy, ReviewedPolicyClause, client_lifecycle_contract,
    engine_integration_contract, reviewed_feature_contracts, transport_contract,
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
pub use module_authoring::{
    ExactTargetArtifactInput, ExactTargetSignoffArtifact, ImplementationClosureEvidence,
    ImplementationClosureGate, ImplementationClosureObservation, ImplementationGateObservation,
    ImplementationGateOutcome, ModuleAuthoringEvidenceAdmission, ModuleAuthoringFormatVersion,
    ModuleAuthoringMapping, ModuleAuthoringReport, ModuleAuthoringScope, ModuleAuthoringScopeInput,
    ModuleAuthority, ModuleEvidenceDomain, ModuleEvidenceObservation, ModuleEvidenceOutcome,
    ModuleEvidencePhase, ModuleImplementationSubject, ModuleSignoffAdmission, ModuleSignoffCase,
    ModuleSignoffCaseObservation, ModuleSignoffCaseOutcome, ModuleSignoffCaseSpec,
    ModuleSignoffExecutionShape, ModuleSignoffManifest, ModuleSignoffObservation,
    ModuleSignoffPhaseTimings, ModuleTerminalStatus, OwnershipCorrection,
    admit_module_authoring_evidence, admit_module_signoff, assemble_implementation_closure,
    build_exact_target_signoff_artifact, build_module_signoff_manifest,
    derive_module_authoring_report, derive_module_authoring_scope, implementation_closure_claims,
    module_authoring_scope_input, required_implementation_closure_gates,
    required_module_signoff_cases,
};
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
    apply_feature_status_changes, parse_feature_scope_declaration,
    validate_downstream_traceability, validate_feature_scope_routing,
    validate_feature_status_changes, validate_ownership_only_correction,
};
pub use transition::{ContractSnapshot, diff_targets, drift_diagnostics};
