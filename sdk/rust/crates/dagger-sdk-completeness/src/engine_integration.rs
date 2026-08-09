//! Exact engine-integration scope, mapping, and observation contracts.
//!
//! This module owns the completeness boundary, not engine behavior. It binds every
//! approved capability to one Rust implementation subject and finite evidence domain,
//! retains delegated module/client content as an explicit non-claim, and rejects scope,
//! target, status, owner, or fingerprint drift before evidence can affect the ledger.

use std::collections::BTreeMap;

use dagger_sdk_engine::EngineEvidenceSubject;
use serde::{Deserialize, Serialize};

use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::feature_scope::FeatureScopePolicy;
use crate::model::{
    CanonicalSet, CapabilityId, Digest, EvidenceId, FeatureId, NonEmptyText, ResolvedLedger,
    Status, TargetDigest,
};

/// Closed Rust implementation owner for one engine-integration capability.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ImplementationSubject {
    /// Built-in SDK metadata and loader resolution.
    BuiltinSdkResolution,
    /// Workspace installation and initialization adapter.
    WorkspaceInitialization,
    /// Pure operation compiler and renderer seams.
    OperationCompiler,
    /// Cargo project adoption and generated-file ownership.
    CargoProjectAdoption,
    /// Runtime reproducibility and container construction policy.
    RuntimeConstruction,
    /// Private runtime protocol probe.
    RuntimeProtocol,
    /// Engine-packaged Rust assets and build provenance.
    EnginePackaging,
    /// Exact-target scope and evidence admission.
    CompletenessEvidence,
}

/// Finite evidence domain which may prove one mapping.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum EngineEvidenceDomain {
    /// Built-in metadata and resolution observations.
    SdkResolution,
    /// Initialization and existing-project preservation observations.
    Initialization,
    /// Pure library operation observations.
    LibraryOperation,
    /// Module code-generation hook observations.
    ModuleOperation,
    /// Standalone client hook observations, excluding final client content.
    ClientHook,
    /// Entrypoint hook observations, excluding general dispatch content.
    EntrypointHook,
    /// Runtime project/build/container observations.
    RuntimeConstruction,
    /// Nested engine protocol-probe observations.
    RuntimeProtocol,
    /// Packaged asset and immutable dependency observations.
    Packaging,
    /// Clone, attachment, cache, and call isolation observations.
    Isolation,
    /// Scope, drift, and evidence-admission observations.
    ScopePolicy,
}

/// Sibling-owned content explicitly excluded from hook evidence.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum DelegatedContentDomain {
    /// General Rust module discovery and function dispatch.
    ModuleAuthoringDispatch,
    /// Complete standalone Rust client contents and usability.
    StandaloneClientContent,
}

/// Direct implementation or reviewed Rust-native replacement of a Go mechanism.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "kebab-case", deny_unknown_fields)]
pub enum EngineMappingDisposition {
    /// The observable capability has a direct Rust implementation subject.
    Direct,
    /// Rust preserves the invariant without copying the Go mechanism or name.
    IdiomaticEquivalent {
        /// Reviewed explanation of the Rust-native replacement.
        rationale: NonEmptyText,
    },
}

/// Terminal classification a complete mapping is permitted to request.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum AllowedTerminalStatus {
    /// Complete direct implementation and exact-target verification.
    Implemented,
    /// Complete reviewed idiomatic replacement and exact-target verification.
    IdiomaticEquivalent,
}

/// One exhaustive mapping from a reviewed capability into Rust implementation evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CapabilityMapping {
    /// Capability identity; duplicate rows are rejected before map construction.
    pub capability_id: CapabilityId,
    /// Approved source fingerprint, preventing name-only mappings.
    pub capability_fingerprint: Digest,
    /// Status that must remain unchanged before implementation evidence exists.
    pub current_status: Status,
    /// Blocking owner expected in the pre-evidence ledger.
    pub blocking_owner: FeatureId,
    /// Rust component responsible for implementation.
    pub implementation_subject: ImplementationSubject,
    /// Non-empty finite evidence set permitted to prove this row.
    pub evidence_domains: CanonicalSet<EngineEvidenceDomain>,
    /// Sibling-owned content that this row cannot close.
    pub delegated_content: CanonicalSet<DelegatedContentDomain>,
    /// Direct or reviewed idiomatic mapping.
    pub disposition: EngineMappingDisposition,
    /// Only status this mapping may request after complete evidence.
    pub allowed_terminal_status: AllowedTerminalStatus,
}

/// Authored exact-target mapping input before duplicate and ledger validation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EngineIntegrationMappings {
    /// Wire-format revision; currently exactly one.
    pub format_version: u32,
    /// Digest of the approved 31 existing capability IDs.
    pub existing_scope_digest: Digest,
    /// Exact checked target for which these mappings are valid.
    pub target_digest: TargetDigest,
    /// Mapping rows retained as a list so duplicate identities remain observable.
    pub mappings: Vec<CapabilityMapping>,
}

/// Duplicate-free, exact-scope mappings safe to use for manifest assembly.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ValidatedEngineIntegrationMappings {
    target_digest: TargetDigest,
    mappings: BTreeMap<CapabilityId, CapabilityMapping>,
}

impl ValidatedEngineIntegrationMappings {
    /// Returns the exact checked target bound to every mapping.
    #[must_use]
    pub const fn target_digest(&self) -> &TargetDigest {
        &self.target_digest
    }

    /// Returns every approved mapping in canonical capability order.
    #[must_use]
    pub const fn mappings(&self) -> &BTreeMap<CapabilityId, CapabilityMapping> {
        &self.mappings
    }
}

/// Validates mapping scope, target, status, ownership, fingerprint, and evidence separation.
pub fn validate_engine_integration_mappings(
    input: &EngineIntegrationMappings,
    ledger: &ResolvedLedger,
    policy: &FeatureScopePolicy,
    target_digest: &TargetDigest,
) -> Validation<ValidatedEngineIntegrationMappings> {
    let mut diagnostics = DiagnosticCollector::default();
    if input.format_version != 1 {
        diagnostics.push(mapping_error(
            "format_version",
            DiagnosticCode::FormatUnsupported,
            "engine-integration mapping format must equal 1",
        ));
    }
    if input.existing_scope_digest != policy.existing_scope_digest {
        diagnostics.push(mapping_error(
            "existing_scope_digest",
            DiagnosticCode::CapabilityMappingInvalid,
            "existing scope digest differs from the approved 31-row fence",
        ));
    }
    if &input.target_digest != target_digest {
        diagnostics.push(mapping_error(
            "target_digest",
            DiagnosticCode::EvidenceTargetMismatch,
            "mapping target differs from the checked target",
        ));
    }

    let expected = policy.capability_ids();
    let mut mappings = BTreeMap::new();
    for mapping in &input.mappings {
        let id = mapping.capability_id.clone();
        if mappings.insert(id.clone(), mapping.clone()).is_some() {
            diagnostics.push(mapping_error(
                id.as_str(),
                DiagnosticCode::CapabilityBindingDuplicate,
                "capability has more than one engine-integration mapping",
            ));
            continue;
        }
        if !expected.contains(&id) {
            diagnostics.push(mapping_error(
                id.as_str(),
                DiagnosticCode::CapabilityMappingInvalid,
                "capability is outside the approved engine-integration scope",
            ));
        }
        let Some(record) = ledger.capabilities.get(&id) else {
            diagnostics.push(mapping_error(
                id.as_str(),
                DiagnosticCode::CapabilityBindingMissing,
                "mapped capability is absent from the current ledger",
            ));
            continue;
        };
        if mapping.capability_fingerprint != record.capability_fingerprint {
            diagnostics.push(mapping_error(
                id.as_str(),
                DiagnosticCode::CapabilityFingerprintMismatch,
                "mapping fingerprint differs from the exact ledger capability",
            ));
        }
        if mapping.current_status != record.status {
            diagnostics.push(mapping_error(
                id.as_str(),
                DiagnosticCode::CapabilityStatusInvalid,
                "mapping changed status before exact implementation evidence exists",
            ));
        }
        if record.owner_feature.as_ref() != Some(&mapping.blocking_owner)
            || mapping.blocking_owner != policy.feature
        {
            diagnostics.push(mapping_error(
                id.as_str(),
                DiagnosticCode::CapabilityOwnerMissing,
                "mapping and ledger must retain the approved blocking owner",
            ));
        }
        if mapping.evidence_domains.is_empty() {
            diagnostics.push(mapping_error(
                id.as_str(),
                DiagnosticCode::CapabilityEvidenceIncomplete,
                "mapping requires at least one finite evidence domain",
            ));
        }
        validate_disposition(mapping, &mut diagnostics);
        validate_delegation(mapping, &mut diagnostics);
    }

    for capability_id in expected.iter() {
        if !mappings.contains_key(capability_id) {
            diagnostics.push(mapping_error(
                capability_id.as_str(),
                DiagnosticCode::CapabilityBindingMissing,
                "approved capability has no engine-integration mapping",
            ));
        }
    }

    diagnostics.finish(ValidatedEngineIntegrationMappings {
        target_digest: input.target_digest.clone(),
        mappings,
    })
}

fn validate_disposition(mapping: &CapabilityMapping, diagnostics: &mut DiagnosticCollector) {
    let valid = matches!(
        (&mapping.disposition, &mapping.allowed_terminal_status),
        (
            EngineMappingDisposition::Direct,
            AllowedTerminalStatus::Implemented
        ) | (
            EngineMappingDisposition::IdiomaticEquivalent { .. },
            AllowedTerminalStatus::IdiomaticEquivalent
        )
    );
    if !valid {
        diagnostics.push(mapping_error(
            mapping.capability_id.as_str(),
            DiagnosticCode::CapabilityMappingInvalid,
            "mapping disposition and permitted terminal status disagree",
        ));
    }
}

fn validate_delegation(mapping: &CapabilityMapping, diagnostics: &mut DiagnosticCollector) {
    for delegated in mapping.delegated_content.iter() {
        let compatible = match delegated {
            DelegatedContentDomain::StandaloneClientContent => mapping
                .evidence_domains
                .contains(&EngineEvidenceDomain::ClientHook),
            DelegatedContentDomain::ModuleAuthoringDispatch => {
                mapping.evidence_domains.iter().any(|domain| {
                    matches!(
                        domain,
                        EngineEvidenceDomain::EntrypointHook
                            | EngineEvidenceDomain::RuntimeProtocol
                    )
                })
            }
        };
        if !compatible {
            diagnostics.push(mapping_error(
                mapping.capability_id.as_str(),
                DiagnosticCode::CapabilityEvidenceIncomplete,
                "delegated content is not paired with its bounded hook evidence domain",
            ));
        }
    }
}

fn mapping_error(subject: &str, code: DiagnosticCode, detail: &'static str) -> ContractDiagnostic {
    ContractDiagnostic::new(code, subject, None, detail)
}

/// Stable identity for one required exact-target integration case.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(transparent)]
pub struct CaseId(NonEmptyText);

/// Result of one required exact-target integration case.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum CaseObservation {
    /// Case passed and produced a stable non-secret observation digest.
    Passed {
        /// Digest of normalized case output.
        observation_digest: Digest,
    },
    /// Case failed with one stable diagnostic identity.
    Failed {
        /// Stable failure code without raw engine output.
        diagnostic: NonEmptyText,
    },
    /// Case did not run and therefore cannot contribute evidence.
    Skipped {
        /// Stable reason suitable for the committed report.
        reason: NonEmptyText,
    },
}

/// Exact mapping manifest assembled before engine observations are admitted.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EngineIntegrationManifest {
    /// Wire-format revision.
    pub format_version: u32,
    /// Approved existing scope digest.
    pub scope_digest: Digest,
    /// Checked target digest.
    pub target_digest: TargetDigest,
    /// Immutable engine source descriptor digest.
    pub engine_source_digest: Digest,
    /// Private packaged-asset digest.
    pub packaged_assets_digest: Digest,
    /// Exact capability mappings in canonical order.
    pub mappings: BTreeMap<CapabilityId, CapabilityMapping>,
}

/// Target-bound observation which can prove only its enumerated capability set.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EngineIntegrationObservation {
    /// Wire-format revision.
    pub format_version: u32,
    /// Stable evidence namespace for one bounded domain.
    pub evidence_id: EvidenceId,
    /// Complete non-secret engine/runtime subject.
    pub subject: EngineEvidenceSubject,
    /// Required cases keyed by stable identity.
    pub cases: BTreeMap<CaseId, CaseObservation>,
    /// Exact capability set this observation claims to prove.
    pub proved_capabilities: CanonicalSet<CapabilityId>,
}
