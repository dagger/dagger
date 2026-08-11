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
    CanonicalSet, CapabilityId, ClassificationValues, Digest, EvidenceId, EvidenceRegistry,
    FeatureId, NonEmptyText, ResolvedLedger, Status, TargetDigest,
};
use crate::traceability::{
    CandidateStatusChanges, FeatureScopeDeclaration, apply_feature_status_changes,
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

impl CaseId {
    /// Constructs a stable case identity after validating its durable spelling.
    pub fn new(value: impl Into<String>) -> Result<Self, crate::model::ValueError> {
        NonEmptyText::new(value).map(Self)
    }

    /// Borrows the validated case identity.
    #[must_use]
    pub fn as_str(&self) -> &str {
        self.0.as_str()
    }
}

impl std::fmt::Display for CaseId {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(self.as_str())
    }
}

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
    /// Complete engine/runtime coordinates which every observation must equal.
    pub expected_subject: EngineEvidenceSubject,
    /// Complete case inventory required in every admitted observation.
    pub required_cases: CanonicalSet<CaseId>,
    /// Source evidence for each mapped Rust implementation subject.
    pub implementation_evidence: BTreeMap<CapabilityId, EvidenceId>,
    /// Reviewed mapping decisions for Rust-native terminal classifications.
    pub decision_evidence: BTreeMap<CapabilityId, EvidenceId>,
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
    /// Finite evidence domain observed by this record.
    pub evidence_domain: EngineEvidenceDomain,
    /// Required cases keyed by stable identity.
    pub cases: BTreeMap<CaseId, CaseObservation>,
    /// Exact capability set this observation claims to prove.
    pub proved_capabilities: CanonicalSet<CapabilityId>,
}

/// Canonical exact-target evidence committed after the complete engine matrix passes.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct EngineIntegrationEvidenceArtifact {
    /// Wire-format revision.
    pub format_version: u32,
    /// Digest of the exact authored mapping bytes used by the run.
    pub mapping_digest: Digest,
    /// Complete immutable admission boundary.
    pub manifest: EngineIntegrationManifest,
    /// Domain-local observations emitted only after every required case passed.
    pub observations: Vec<EngineIntegrationObservation>,
}

/// Capability-local result of atomically admitting engine observations.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EngineIntegrationEvidenceClosure {
    complete_capabilities: CanonicalSet<CapabilityId>,
    verification_evidence: BTreeMap<CapabilityId, CanonicalSet<EvidenceId>>,
    observed_domains: BTreeMap<CapabilityId, CanonicalSet<EngineEvidenceDomain>>,
    missing_domains: BTreeMap<CapabilityId, CanonicalSet<EngineEvidenceDomain>>,
}

impl EngineIntegrationEvidenceClosure {
    /// Returns capabilities for which every declared evidence domain was observed.
    #[must_use]
    pub const fn complete_capabilities(&self) -> &CanonicalSet<CapabilityId> {
        &self.complete_capabilities
    }

    /// Returns the admitted verification identities for each claimed capability.
    #[must_use]
    pub const fn verification_evidence(&self) -> &BTreeMap<CapabilityId, CanonicalSet<EvidenceId>> {
        &self.verification_evidence
    }

    /// Returns the exact observed evidence-domain projection.
    #[must_use]
    pub const fn observed_domains(
        &self,
    ) -> &BTreeMap<CapabilityId, CanonicalSet<EngineEvidenceDomain>> {
        &self.observed_domains
    }

    /// Returns missing domain identities without relabelling the retained blockers.
    #[must_use]
    pub const fn missing_domains(
        &self,
    ) -> &BTreeMap<CapabilityId, CanonicalSet<EngineEvidenceDomain>> {
        &self.missing_domains
    }
}

/// Feature 1 transition result plus its exact remaining engine-evidence blockers.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EngineIntegrationTransition {
    /// Ledger produced by the existing completeness transition policy.
    pub ledger: ResolvedLedger,
    /// Candidate changes submitted to that policy.
    pub candidate: CandidateStatusChanges,
    /// Missing local evidence domains for rows that remained blocking.
    pub remaining_domains: BTreeMap<CapabilityId, CanonicalSet<EngineEvidenceDomain>>,
}

/// Assembles the immutable boundary used to admit exact-target engine observations.
pub fn assemble_engine_integration_manifest(
    mappings: &ValidatedEngineIntegrationMappings,
    policy: &FeatureScopePolicy,
    expected_subject: EngineEvidenceSubject,
    required_cases: CanonicalSet<CaseId>,
    implementation_evidence: BTreeMap<CapabilityId, EvidenceId>,
    decision_evidence: BTreeMap<CapabilityId, EvidenceId>,
) -> Validation<EngineIntegrationManifest> {
    let mut diagnostics = DiagnosticCollector::default();
    let mapped_capabilities = CanonicalSet::new(mappings.mappings.keys().cloned());
    let implementation_capabilities = CanonicalSet::new(implementation_evidence.keys().cloned());

    if mapped_capabilities != policy.capability_ids() {
        diagnostics.push(evidence_error(
            "mappings",
            DiagnosticCode::CapabilityMappingInvalid,
            "manifest mappings differ from the approved engine-integration scope",
        ));
    }
    if implementation_capabilities != mapped_capabilities {
        diagnostics.push(evidence_error(
            "implementation_evidence",
            DiagnosticCode::ImplementationEvidenceMissing,
            "implementation evidence must cover every mapped capability exactly",
        ));
    }
    let idiomatic_capabilities = CanonicalSet::new(
        mappings
            .mappings
            .iter()
            .filter(|(_, mapping)| {
                matches!(
                    mapping.allowed_terminal_status,
                    AllowedTerminalStatus::IdiomaticEquivalent
                )
            })
            .map(|(capability_id, _)| capability_id.clone()),
    );
    if CanonicalSet::new(decision_evidence.keys().cloned()) != idiomatic_capabilities {
        diagnostics.push(evidence_error(
            "decision_evidence",
            DiagnosticCode::DecisionEvidenceInvalid,
            "reviewed decision evidence must cover exactly the idiomatic mappings",
        ));
    }
    if required_cases.is_empty() {
        diagnostics.push(evidence_error(
            "required_cases",
            DiagnosticCode::EvidenceOutcomeMissing,
            "engine-integration manifest requires a non-empty exact case inventory",
        ));
    }

    let engine_source_digest =
        Digest::new(expected_subject.engine_source_digest.as_str()).expect("shared digest grammar");
    let packaged_assets_digest = Digest::new(expected_subject.packaged_assets_digest.as_str())
        .expect("shared digest grammar");
    diagnostics.finish(EngineIntegrationManifest {
        format_version: 1,
        scope_digest: policy.existing_scope_digest.clone(),
        target_digest: mappings.target_digest.clone(),
        engine_source_digest,
        packaged_assets_digest,
        expected_subject,
        required_cases,
        implementation_evidence,
        decision_evidence,
        mappings: mappings.mappings.clone(),
    })
}

/// Atomically admits exact-subject observations and derives capability-local closure.
pub fn verify_engine_integration_evidence(
    manifest: &EngineIntegrationManifest,
    observations: &[EngineIntegrationObservation],
) -> Validation<EngineIntegrationEvidenceClosure> {
    let mut diagnostics = DiagnosticCollector::default();
    validate_manifest_integrity(manifest, &mut diagnostics);
    let required_cases = manifest.required_cases.clone();
    let mut seen_evidence = std::collections::BTreeSet::new();
    let mut evidence_by_capability = BTreeMap::<CapabilityId, Vec<EvidenceId>>::new();
    let mut domains_by_capability = BTreeMap::<CapabilityId, Vec<EngineEvidenceDomain>>::new();

    for observation in observations {
        let subject = observation.evidence_id.as_str();
        if observation.format_version != 1 {
            diagnostics.push(evidence_error(
                subject,
                DiagnosticCode::FormatUnsupported,
                "engine-integration observation format must equal 1",
            ));
        }
        if !seen_evidence.insert(observation.evidence_id.clone()) {
            diagnostics.push(evidence_error(
                subject,
                DiagnosticCode::CapabilityBindingDuplicate,
                "engine-integration evidence identity is duplicated",
            ));
        }
        if observation.subject != manifest.expected_subject {
            diagnostics.push(evidence_error(
                subject,
                DiagnosticCode::EvidenceSubjectMismatch,
                "observation subject differs from the complete manifest subject",
            ));
        }

        let observed_cases = CanonicalSet::new(observation.cases.keys().cloned());
        if observed_cases != required_cases
            || observation
                .cases
                .values()
                .any(|result| !matches!(result, CaseObservation::Passed { .. }))
        {
            diagnostics.push(evidence_error(
                subject,
                DiagnosticCode::EvidenceOutcomeMissing,
                "observation must contain the complete passing case inventory",
            ));
        }
        if observation.proved_capabilities.is_empty() {
            diagnostics.push(evidence_error(
                subject,
                DiagnosticCode::CapabilityEvidenceIncomplete,
                "observation must prove at least one mapped capability",
            ));
        }

        for capability_id in observation.proved_capabilities.iter() {
            let Some(mapping) = manifest.mappings.get(capability_id) else {
                diagnostics.push(evidence_error(
                    capability_id,
                    DiagnosticCode::CapabilityMappingInvalid,
                    "observation claims an out-of-scope capability",
                ));
                continue;
            };
            if !mapping
                .evidence_domains
                .contains(&observation.evidence_domain)
            {
                diagnostics.push(evidence_error(
                    capability_id,
                    DiagnosticCode::CapabilityEvidenceIncomplete,
                    "observation domain is not declared for this capability",
                ));
                continue;
            }
            evidence_by_capability
                .entry(capability_id.clone())
                .or_default()
                .push(observation.evidence_id.clone());
            domains_by_capability
                .entry(capability_id.clone())
                .or_default()
                .push(observation.evidence_domain.clone());
        }
    }

    let mut verification_evidence = BTreeMap::new();
    let mut observed_domains = BTreeMap::new();
    let mut missing_domains = BTreeMap::new();
    let mut complete_capabilities = Vec::new();
    for (capability_id, mapping) in &manifest.mappings {
        let evidence = CanonicalSet::new(
            evidence_by_capability
                .remove(capability_id)
                .unwrap_or_default(),
        );
        let observed = CanonicalSet::new(
            domains_by_capability
                .remove(capability_id)
                .unwrap_or_default(),
        );
        let missing = CanonicalSet::new(
            mapping
                .evidence_domains
                .iter()
                .filter(|domain| !observed.contains(domain))
                .cloned(),
        );
        if missing.is_empty() {
            complete_capabilities.push(capability_id.clone());
        } else {
            missing_domains.insert(capability_id.clone(), missing);
        }
        verification_evidence.insert(capability_id.clone(), evidence);
        observed_domains.insert(capability_id.clone(), observed);
    }

    diagnostics.finish(EngineIntegrationEvidenceClosure {
        complete_capabilities: CanonicalSet::new(complete_capabilities),
        verification_evidence,
        observed_domains,
        missing_domains,
    })
}

/// Builds only evidence-complete candidate rows for the existing transition policy.
#[must_use]
pub fn derive_engine_integration_status_changes(
    manifest: &EngineIntegrationManifest,
    closure: &EngineIntegrationEvidenceClosure,
) -> CandidateStatusChanges {
    let changes = closure
        .complete_capabilities
        .iter()
        .map(|capability_id| {
            let mapping = &manifest.mappings[capability_id];
            let status = match mapping.allowed_terminal_status {
                AllowedTerminalStatus::Implemented => Status::Implemented,
                AllowedTerminalStatus::IdiomaticEquivalent => Status::IdiomaticEquivalent,
            };
            (
                capability_id.clone(),
                ClassificationValues {
                    status,
                    gap: None,
                    owner_feature: None,
                    implementation_evidence: CanonicalSet::new([manifest.implementation_evidence
                        [capability_id]
                        .clone()]),
                    verification_evidence: closure.verification_evidence[capability_id].clone(),
                    decision_evidence: manifest
                        .decision_evidence
                        .get(capability_id)
                        .cloned()
                        .map_or_else(CanonicalSet::default, |evidence_id| {
                            CanonicalSet::new([evidence_id])
                        }),
                },
            )
        })
        .collect();
    CandidateStatusChanges { changes }
}

/// Applies admitted engine evidence exclusively through the Feature 1 transition API.
#[allow(clippy::too_many_arguments)]
pub fn apply_engine_integration_statuses(
    current: &ResolvedLedger,
    declaration: &FeatureScopeDeclaration,
    policy: &FeatureScopePolicy,
    manifest: &EngineIntegrationManifest,
    observations: &[EngineIntegrationObservation],
    candidate_evidence: &EvidenceRegistry,
    target: &TargetDigest,
) -> Validation<EngineIntegrationTransition> {
    let closure = verify_engine_integration_evidence(manifest, observations)?;
    let candidate = derive_engine_integration_status_changes(manifest, &closure);
    let ledger = apply_feature_status_changes(
        current,
        declaration,
        policy,
        &candidate,
        candidate_evidence,
        target,
        &BTreeMap::new(),
        false,
    )?;
    Ok(EngineIntegrationTransition {
        ledger,
        candidate,
        remaining_domains: closure.missing_domains,
    })
}

fn validate_manifest_integrity(
    manifest: &EngineIntegrationManifest,
    diagnostics: &mut DiagnosticCollector,
) {
    if manifest.format_version != 1 {
        diagnostics.push(evidence_error(
            "format_version",
            DiagnosticCode::FormatUnsupported,
            "engine-integration manifest format must equal 1",
        ));
    }
    let expected_engine = manifest.expected_subject.engine_source_digest.as_str();
    let expected_assets = manifest.expected_subject.packaged_assets_digest.as_str();
    if manifest.engine_source_digest.as_str() != expected_engine
        || manifest.packaged_assets_digest.as_str() != expected_assets
    {
        diagnostics.push(evidence_error(
            "expected_subject",
            DiagnosticCode::EvidenceSubjectMismatch,
            "manifest digest projection differs from its complete expected subject",
        ));
    }
    let mapped = CanonicalSet::new(manifest.mappings.keys().cloned());
    let implemented = CanonicalSet::new(manifest.implementation_evidence.keys().cloned());
    let idiomatic = CanonicalSet::new(
        manifest
            .mappings
            .iter()
            .filter(|(_, mapping)| {
                matches!(
                    mapping.allowed_terminal_status,
                    AllowedTerminalStatus::IdiomaticEquivalent
                )
            })
            .map(|(capability_id, _)| capability_id.clone()),
    );
    let decided = CanonicalSet::new(manifest.decision_evidence.keys().cloned());
    if mapped != implemented || idiomatic != decided || manifest.required_cases.is_empty() {
        diagnostics.push(evidence_error(
            "manifest",
            DiagnosticCode::CapabilityEvidenceIncomplete,
            "manifest requires exact implementation evidence and a non-empty case set",
        ));
    }
}

fn evidence_error(
    subject: impl ToString,
    code: DiagnosticCode,
    detail: &'static str,
) -> ContractDiagnostic {
    ContractDiagnostic::new(code, subject.to_string(), None, detail)
}
