//! Exact standalone-client capability scope and evidence admission.
//!
//! This boundary keeps the one retained authority row, the reviewed Rust-policy rows,
//! the ownership-only provision correction, and the engine-hook ownership fence in one
//! closed model. Evidence must match the complete set for its domain before any status
//! change is exposed; a rejection always retains every unresolved blocker.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    CapabilityId, Digest, EvidenceId, FeatureId, NonEmptyText, ResolvedLedger, Status, TargetDigest,
};

const INITIALIZATION_ID: &str = "behavior/go-client/init-client-lifecycle";
const INITIALIZATION_FINGERPRINT: &str =
    "sha256:1dfbf33549038de9fd9fbac8a12574d88658764e0c9732cd5a7996d14a3beb37";
const PROVISION_ID: &str =
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2550rovision";
const PROVISION_FINGERPRINT: &str =
    "sha256:42cf3a1fb160841bd3237cbf44dd394c9fff5d661a9361b44a518b34e1bde26d";

/// Strict standalone-client scope/evidence wire format.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ClientGenerationFormatVersion(u32);

impl ClientGenerationFormatVersion {
    /// Returns the only accepted scope/evidence format.
    #[must_use]
    pub const fn current() -> Self {
        Self(1)
    }
}

impl Serialize for ClientGenerationFormatVersion {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: serde::Serializer,
    {
        serializer.serialize_u32(self.0)
    }
}

impl<'de> Deserialize<'de> for ClientGenerationFormatVersion {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        match u32::deserialize(deserializer)? {
            1 => Ok(Self::current()),
            _ => Err(serde::de::Error::custom(
                "unsupported client generation format version",
            )),
        }
    }
}

/// Authority owning the observable behaviour represented by one mapping.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientAuthority {
    /// Definitive Go client lifecycle behaviour.
    GoClient,
    /// Target engine workspace, schema, or SDK ABI behaviour.
    Engine,
    /// Reviewed Rust-native ownership and ergonomics policy.
    RustPolicy,
}

/// Closed implementation subject responsible for one client capability.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientImplementationSubject {
    /// Client initialization adapter and scaffold planner.
    Initialization,
    /// Workspace record, cwd, pin, and multi-client selection.
    WorkspaceSelection,
    /// Core-plus-one-module schema scope compiler.
    SchemaCompiler,
    /// Exact public Core/runtime reuse.
    CoreComposition,
    /// Namespaced generated module API.
    ModuleApi,
    /// Cargo project discovery and semantic reconciliation.
    ProjectReconciliation,
    /// Manifest-authorized deterministic publication.
    Publication,
    /// Generated Core and module query execution.
    QueryRuntime,
    /// Stable diagnostic, source, and credential policy.
    DiagnosticSecurity,
    /// Engine-free checkpoint and evidence boundary.
    EvidenceBoundary,
}

/// Finite observation domains permitted to prove standalone-client claims.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientEvidenceDomain {
    /// Direct Rust-owned Go adapter fixture.
    AdapterFixture,
    /// Pure workspace selection and pin property.
    WorkspaceProperty,
    /// Pure client-visible schema property.
    SchemaProperty,
    /// Generated API catalog and compile property.
    GeneratedApiProperty,
    /// Cargo discovery and reconciliation property.
    ProjectProperty,
    /// Ownership and failure-atomic publication property.
    PublicationProperty,
    /// Recording-transport query property.
    QueryTransportProperty,
    /// Diagnostic, source, package, and security hygiene.
    DiagnosticSecurity,
    /// Complete engine-free local closure.
    ImplementationClosure,
    /// Deferred exact-target engine sign-off.
    ExactEngineSignoff,
    /// Feature 5 hook-only evidence, never sufficient for client contents.
    EngineHook,
}

/// Terminal status one complete mapping is permitted to request.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientTerminalStatus {
    /// Direct complete implementation.
    Implemented,
    /// Complete Rust-native behavioural equivalent.
    IdiomaticEquivalent,
}

/// Report section retaining one distinct class of unresolved client blockers.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientReportSection {
    /// Engine and adapter initialization lifecycle.
    Initialization,
    /// Schema and generated module contents.
    GeneratedContent,
    /// Cargo adoption and immutable dependency policy.
    CargoIntegration,
    /// Ownership, preservation, and regeneration.
    Regeneration,
    /// Generated Core and module query usability.
    QueryUsability,
    /// Engine-free implementation closure.
    LocalClosure,
    /// Deferred exact-engine sign-off.
    SdkSignoff,
}

/// One exact capability-to-client implementation mapping.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientGenerationMapping {
    /// Stable capability identity.
    pub capability_id: CapabilityId,
    /// Reviewed capability fingerprint.
    pub capability_fingerprint: Digest,
    /// Owning behavioural authority.
    pub authority: ClientAuthority,
    /// One approved requirement coordinate.
    pub requirement: NonEmptyText,
    /// Concrete Rust implementation subject.
    pub implementation_subject: ClientImplementationSubject,
    /// Reviewed behavioural rationale.
    pub rationale: NonEmptyText,
    /// Non-empty finite evidence-domain set.
    pub evidence_domains: BTreeSet<ClientEvidenceDomain>,
    /// Only final status this mapping may request.
    pub allowed_terminal_status: ClientTerminalStatus,
    /// Report section which must retain the row while unproved.
    pub report_section: ClientReportSection,
    /// Exact target to which the mapping applies.
    pub target_digest: TargetDigest,
    /// Whether an unproved row remains blocking.
    pub blocker: bool,
}

/// Ownership-only correction for the pinned Go `TestProvision` row.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientOwnershipCorrection {
    /// Stable pinned capability identity.
    pub capability_id: CapabilityId,
    /// Fingerprint which must remain byte-identical through the correction.
    pub capability_fingerprint: Digest,
    /// Status which must remain unchanged through the correction.
    pub status: Status,
    /// Prior coarse owner.
    pub from: FeatureId,
    /// Correct transport/provisioning owner.
    pub to: FeatureId,
}

/// Feature 5 boundary row which client evidence may not absorb.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PreservedClientBoundary {
    /// Stable capability identity.
    pub capability_id: CapabilityId,
    /// Pinned capability fingerprint.
    pub capability_fingerprint: Digest,
    /// Existing status retained without promotion.
    pub status: Status,
    /// Required owning feature.
    pub owner: FeatureId,
}

/// Approved dependency scope for one generated standalone client.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientDependencyScope {
    /// Complete Core plus exactly one selected local or pinned remote module.
    CorePlusOneBoundModule,
    /// Invalid legacy interpretation which merged transitive dependencies.
    MergedTransitiveGraph,
}

/// Authored scope input retained as lists so duplicates remain observable.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientGenerationScopeInput {
    /// Strict scope format.
    pub format_version: ClientGenerationFormatVersion,
    /// Exact target shared by all mappings.
    pub target_digest: TargetDigest,
    /// Retained initialization and added policy mappings.
    pub mappings: Vec<ClientGenerationMapping>,
    /// Exact ownership-only provision correction.
    pub ownership_corrections: Vec<ClientOwnershipCorrection>,
    /// Exact hook and operation rows which remain owned by Feature 5.
    pub preserved_boundaries: Vec<PreservedClientBoundary>,
    /// One-client dependency interpretation reviewed against engine behaviour.
    pub dependency_scope: ClientDependencyScope,
}

/// Duplicate-free exact client-generation scope safe for evidence admission.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientGenerationScope {
    target_digest: TargetDigest,
    mapping_digest: Digest,
    mappings: BTreeMap<CapabilityId, ClientGenerationMapping>,
    ownership_correction: ClientOwnershipCorrection,
    preserved_boundaries: BTreeMap<CapabilityId, PreservedClientBoundary>,
}

impl ClientGenerationScope {
    /// Returns the exact target bound to every mapping.
    #[must_use]
    pub const fn target_digest(&self) -> &TargetDigest {
        &self.target_digest
    }

    /// Returns the canonical complete mapping identity.
    #[must_use]
    pub const fn mapping_digest(&self) -> &Digest {
        &self.mapping_digest
    }

    /// Returns every retained and Rust-policy mapping in canonical identity order.
    #[must_use]
    pub const fn mappings(&self) -> &BTreeMap<CapabilityId, ClientGenerationMapping> {
        &self.mappings
    }

    /// Returns the ownership-only provisioning correction.
    #[must_use]
    pub const fn ownership_correction(&self) -> &ClientOwnershipCorrection {
        &self.ownership_correction
    }

    /// Returns Feature 5 rows excluded from generated-content evidence.
    #[must_use]
    pub const fn preserved_boundaries(&self) -> &BTreeMap<CapabilityId, PreservedClientBoundary> {
        &self.preserved_boundaries
    }

    /// Returns all currently unproved blocking identities.
    pub fn blockers(&self) -> BTreeSet<CapabilityId> {
        self.mappings
            .values()
            .filter(|mapping| mapping.blocker)
            .map(|mapping| mapping.capability_id.clone())
            .collect()
    }
}

/// Stable completeness failure code for standalone-client scope and evidence.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ClientGenerationDiagnosticCode {
    /// Mapping, fingerprint, ownership, or dependency scope differs from review.
    CapabilityScopeChanged,
    /// Evidence is stale, incomplete, failed, skipped, or outside its allowed domain.
    CapabilityEvidenceIncomplete,
    /// Engine-free closure evidence is absent or inconsistent.
    ClientClosureIncomplete,
    /// Exact-target sign-off evidence is absent or inconsistent.
    ClientSignoffIncomplete,
    /// Exact-target sign-off rebuilt or restarted a reusable resource.
    ClientSignoffDuplicateWork,
}

/// One stable, safely located standalone-client completeness diagnostic.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientGenerationDiagnostic {
    /// Stable machine-readable failure class.
    pub code: ClientGenerationDiagnosticCode,
    /// Optional non-secret capability or evidence coordinate.
    pub coordinate: Option<NonEmptyText>,
    /// Bounded fixed explanation which contains no source text or credentials.
    pub message: NonEmptyText,
}

/// Non-empty deterministically ordered client completeness diagnostics.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientGenerationDiagnosticSet(Vec<ClientGenerationDiagnostic>);

impl ClientGenerationDiagnosticSet {
    fn one(code: ClientGenerationDiagnosticCode, message: &'static str) -> Self {
        Self(vec![ClientGenerationDiagnostic {
            code,
            coordinate: None,
            message: text(message),
        }])
    }

    /// Borrows diagnostics in stable code/coordinate/message order.
    #[must_use]
    pub fn diagnostics(&self) -> &[ClientGenerationDiagnostic] {
        &self.0
    }

    /// Reports whether the set contains a stable failure code.
    #[must_use]
    pub fn contains(&self, code: ClientGenerationDiagnosticCode) -> bool {
        self.0.iter().any(|diagnostic| diagnostic.code == code)
    }
}

/// Evidence outcome; only `Passed` can request a status transition.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ClientEvidenceOutcome {
    /// Observation completed successfully.
    Passed { observation_digest: Digest },
    /// Observation ran and failed.
    Failed { diagnostic: NonEmptyText },
    /// Observation did not execute.
    Skipped { reason: NonEmptyText },
}

/// Strict target- and mapping-bound client evidence observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientEvidenceObservation {
    /// Strict evidence format.
    pub format_version: ClientGenerationFormatVersion,
    /// Stable evidence identity.
    pub evidence_id: EvidenceId,
    /// Exact checked target.
    pub target_digest: TargetDigest,
    /// Complete mapping identity observed by the producer.
    pub mapping_digest: Digest,
    /// Finite observation domain.
    pub domain: ClientEvidenceDomain,
    /// Authored list retained so duplicates remain observable.
    pub capability_ids: Vec<CapabilityId>,
    /// Pass/fail/skip outcome.
    pub result: ClientEvidenceOutcome,
}

/// Report which keeps distinct client evidence phases visible.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientGenerationReport {
    /// Unproved blockers partitioned into durable reviewer-facing sections.
    pub blockers: BTreeMap<ClientReportSection, BTreeSet<CapabilityId>>,
}

/// Evidence admission result; every rejection is an explicit no-op.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientEvidenceAdmission {
    /// Permitted status changes; empty for every rejection.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Every blocker remaining after this one observation.
    pub report: ClientGenerationReport,
    /// Stable rejection reason, absent only for admitted evidence.
    pub rejection: Option<ClientGenerationDiagnosticSet>,
}

/// Constructs the reviewed one-authority/24-policy client-generation scope.
pub fn client_generation_scope_input(target_digest: TargetDigest) -> ClientGenerationScopeInput {
    let mut mappings = Vec::with_capacity(1 + POLICY_SPECS.len());
    mappings.push(initialization_mapping(&target_digest));
    mappings.extend(
        POLICY_SPECS
            .iter()
            .map(|spec| policy_mapping(spec, &target_digest)),
    );
    ClientGenerationScopeInput {
        format_version: ClientGenerationFormatVersion::current(),
        target_digest,
        mappings,
        ownership_corrections: vec![expected_ownership_correction()],
        preserved_boundaries: expected_boundaries(),
        dependency_scope: ClientDependencyScope::CorePlusOneBoundModule,
    }
}

/// Validates exact set membership, fingerprints, ownership, target, and dependency scope.
pub fn derive_client_generation_scope(
    input: &ClientGenerationScopeInput,
    expected_target: &TargetDigest,
) -> Result<ClientGenerationScope, ClientGenerationDiagnosticSet> {
    if &input.target_digest != expected_target
        || input.dependency_scope != ClientDependencyScope::CorePlusOneBoundModule
    {
        return Err(scope_error());
    }
    let expected = client_generation_scope_input(expected_target.clone());
    let mappings = unique_map(&input.mappings, |mapping| mapping.capability_id.clone())
        .ok_or_else(scope_error)?;
    let expected_mappings = unique_map(&expected.mappings, |mapping| mapping.capability_id.clone())
        .ok_or_else(scope_error)?;
    if mappings != expected_mappings {
        return Err(scope_error());
    }
    if input.ownership_corrections.as_slice() != expected.ownership_corrections.as_slice() {
        return Err(scope_error());
    }
    let preserved_boundaries =
        unique_map(&input.preserved_boundaries, |row| row.capability_id.clone())
            .ok_or_else(scope_error)?;
    let expected_boundaries = unique_map(&expected.preserved_boundaries, |row| {
        row.capability_id.clone()
    })
    .ok_or_else(scope_error)?;
    if preserved_boundaries != expected_boundaries {
        return Err(scope_error());
    }
    let mapping_digest = canonical_digest(
        DigestDomain::ClientGeneration,
        &(
            &input.target_digest,
            &mappings,
            &input.ownership_corrections,
            &preserved_boundaries,
            input.dependency_scope,
        ),
    )
    .map_err(|_| scope_error())?;
    Ok(ClientGenerationScope {
        target_digest: input.target_digest.clone(),
        mapping_digest,
        mappings,
        ownership_correction: input.ownership_corrections[0].clone(),
        preserved_boundaries,
    })
}

/// Applies the reviewed `TestProvision` ownership-only correction.
///
/// The baseline remains independently reproducible as Feature 7 input. This transition
/// validates its immutable capability projection before changing only the final owner.
pub fn apply_client_ownership_correction(
    before: &ResolvedLedger,
    scope: &ClientGenerationScope,
) -> Result<ResolvedLedger, ClientGenerationDiagnosticSet> {
    let correction = scope.ownership_correction();
    let Some(current) = before.capabilities.get(&correction.capability_id) else {
        return Err(scope_error());
    };
    if current.capability_fingerprint != correction.capability_fingerprint
        || current.status != correction.status
        || current.owner_feature.as_ref() != Some(&correction.from)
    {
        return Err(scope_error());
    }
    let mut corrected = before.clone();
    let Some(row) = corrected.capabilities.get_mut(&correction.capability_id) else {
        return Err(scope_error());
    };
    row.owner_feature = Some(correction.to.clone());
    Ok(corrected)
}

/// Admits exactly one complete passed evidence-domain claim set.
#[must_use]
pub fn admit_client_evidence(
    scope: &ClientGenerationScope,
    observation: &ClientEvidenceObservation,
) -> ClientEvidenceAdmission {
    let rejected = || ClientEvidenceAdmission {
        status_changes: BTreeMap::new(),
        report: report(scope, &BTreeSet::new()),
        rejection: Some(ClientGenerationDiagnosticSet::one(
            ClientGenerationDiagnosticCode::CapabilityEvidenceIncomplete,
            "client evidence is stale, incomplete, unsuccessful, or outside its reviewed domain",
        )),
    };
    if observation.target_digest != *scope.target_digest()
        || observation.mapping_digest != *scope.mapping_digest()
        || !matches!(observation.result, ClientEvidenceOutcome::Passed { .. })
        || observation.domain == ClientEvidenceDomain::EngineHook
    {
        return rejected();
    }
    let claimed = observation
        .capability_ids
        .iter()
        .cloned()
        .collect::<BTreeSet<_>>();
    if claimed.len() != observation.capability_ids.len() {
        return rejected();
    }
    let expected = scope
        .mappings
        .values()
        .filter(|mapping| mapping.evidence_domains.contains(&observation.domain))
        .map(|mapping| mapping.capability_id.clone())
        .collect::<BTreeSet<_>>();
    if expected.is_empty() || claimed != expected {
        return rejected();
    }
    let status_changes = expected
        .iter()
        .map(|capability_id| {
            let status = match scope.mappings[capability_id].allowed_terminal_status {
                ClientTerminalStatus::Implemented => Status::Implemented,
                ClientTerminalStatus::IdiomaticEquivalent => Status::IdiomaticEquivalent,
            };
            (capability_id.clone(), status)
        })
        .collect();
    ClientEvidenceAdmission {
        status_changes,
        report: report(scope, &expected),
        rejection: None,
    }
}

fn report(
    scope: &ClientGenerationScope,
    proved: &BTreeSet<CapabilityId>,
) -> ClientGenerationReport {
    let mut blockers = BTreeMap::new();
    for section in [
        ClientReportSection::Initialization,
        ClientReportSection::GeneratedContent,
        ClientReportSection::CargoIntegration,
        ClientReportSection::Regeneration,
        ClientReportSection::QueryUsability,
        ClientReportSection::LocalClosure,
        ClientReportSection::SdkSignoff,
    ] {
        blockers.insert(section, BTreeSet::new());
    }
    for mapping in scope.mappings.values() {
        if mapping.blocker && !proved.contains(&mapping.capability_id) {
            blockers
                .entry(mapping.report_section)
                .or_default()
                .insert(mapping.capability_id.clone());
        }
    }
    ClientGenerationReport { blockers }
}

fn unique_map<T: Clone>(
    values: &[T],
    key: impl Fn(&T) -> CapabilityId,
) -> Option<BTreeMap<CapabilityId, T>> {
    let mut mapped = BTreeMap::new();
    for value in values {
        if mapped.insert(key(value), value.clone()).is_some() {
            return None;
        }
    }
    Some(mapped)
}

fn initialization_mapping(target_digest: &TargetDigest) -> ClientGenerationMapping {
    ClientGenerationMapping {
        capability_id: capability(INITIALIZATION_ID),
        capability_fingerprint: digest(INITIALIZATION_FINGERPRINT),
        authority: ClientAuthority::GoClient,
        requirement: text("1.1"),
        implementation_subject: ClientImplementationSubject::Initialization,
        rationale: text("The definitive lifecycle remains engine-signoff gated."),
        evidence_domains: BTreeSet::from([ClientEvidenceDomain::ExactEngineSignoff]),
        allowed_terminal_status: ClientTerminalStatus::Implemented,
        report_section: ClientReportSection::Initialization,
        target_digest: target_digest.clone(),
        blocker: true,
    }
}

fn policy_mapping(spec: &PolicySpec, target_digest: &TargetDigest) -> ClientGenerationMapping {
    let fingerprint = Digest::sha256(format!(
        "dagger-rust-client-policy-v1\0{}\0{}\0{}",
        spec.id, spec.requirement, spec.rationale
    ));
    ClientGenerationMapping {
        capability_id: capability(spec.id),
        capability_fingerprint: fingerprint,
        authority: spec.authority,
        requirement: text(spec.requirement),
        implementation_subject: spec.subject,
        rationale: text(spec.rationale),
        evidence_domains: BTreeSet::from([spec.domain]),
        allowed_terminal_status: ClientTerminalStatus::Implemented,
        report_section: spec.section,
        target_digest: target_digest.clone(),
        blocker: true,
    }
}

fn expected_ownership_correction() -> ClientOwnershipCorrection {
    ClientOwnershipCorrection {
        capability_id: capability(PROVISION_ID),
        capability_fingerprint: digest(PROVISION_FINGERPRINT),
        status: Status::Partial,
        from: FeatureId::Feature7,
        to: FeatureId::Feature3,
    }
}

fn expected_boundaries() -> Vec<PreservedClientBoundary> {
    [
        (
            "behavior/go-codegen/source%2Fgo-codegen%2Fgo-method%2Fgogenerator%2F%2547o%2547enerator%2F%2547enerate%2543lient",
            "sha256:092288e4ae793c94fef04442d33c1f8b7ae0e3bf7f184570c288fa0250496619",
            Status::Partial,
        ),
        (
            "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Fgenerator%2F%2547enerator",
            "sha256:20591cad1b89a97fe13b1b41dba336f8c1108c9ff2dda8478528338e99db55a2",
            Status::Partial,
        ),
        (
            "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2543lient%2547enerator",
            "sha256:ab8c33f9dfcd8202538965ed2911afeca9d257234f3129804fb27740a90e9646",
            Status::Missing,
        ),
    ]
    .into_iter()
    .map(|(id, fingerprint, status)| PreservedClientBoundary {
        capability_id: capability(id),
        capability_fingerprint: digest(fingerprint),
        status,
        owner: FeatureId::Feature5,
    })
    .collect()
}

fn scope_error() -> ClientGenerationDiagnosticSet {
    ClientGenerationDiagnosticSet::one(
        ClientGenerationDiagnosticCode::CapabilityScopeChanged,
        "client capability scope, target, ownership, fingerprint, or dependency policy differs from review",
    )
}

fn capability(value: &str) -> CapabilityId {
    CapabilityId::new(value).expect("reviewed client capability identity is valid")
}

fn digest(value: &str) -> Digest {
    Digest::new(value).expect("reviewed client capability fingerprint is valid")
}

fn text(value: &str) -> NonEmptyText {
    NonEmptyText::new(value).expect("reviewed client mapping text is non-empty")
}

#[derive(Clone, Copy)]
struct PolicySpec {
    id: &'static str,
    requirement: &'static str,
    authority: ClientAuthority,
    subject: ClientImplementationSubject,
    domain: ClientEvidenceDomain,
    section: ClientReportSection,
    rationale: &'static str,
}

const POLICY_SPECS: &[PolicySpec] = &[
    policy(
        "policy/rust-policy/client-capability-scope",
        "1.3-1.12",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::EvidenceBoundary,
        ClientEvidenceDomain::ImplementationClosure,
        ClientReportSection::LocalClosure,
        "Capability ownership and evidence remain exact-set gated.",
    ),
    policy(
        "policy/rust-policy/client-initialization",
        "2.1-2.8",
        ClientAuthority::Engine,
        ClientImplementationSubject::Initialization,
        ClientEvidenceDomain::AdapterFixture,
        ClientReportSection::Initialization,
        "Initialization creates or adopts only a confined Cargo scaffold.",
    ),
    policy(
        "policy/rust-policy/client-scoped-initial-generation",
        "2.9-2.11",
        ClientAuthority::Engine,
        ClientImplementationSubject::WorkspaceSelection,
        ClientEvidenceDomain::WorkspaceProperty,
        ClientReportSection::Initialization,
        "Initial generation is limited to the newly recorded client.",
    ),
    policy(
        "policy/rust-policy/client-workspace-record",
        "3.1-3.2",
        ClientAuthority::Engine,
        ClientImplementationSubject::WorkspaceSelection,
        ClientEvidenceDomain::WorkspaceProperty,
        ClientReportSection::GeneratedContent,
        "The engine record selects one client root and module reference.",
    ),
    policy(
        "policy/rust-policy/client-pinned-module-resolution",
        "3.2-3.4",
        ClientAuthority::Engine,
        ClientImplementationSubject::WorkspaceSelection,
        ClientEvidenceDomain::WorkspaceProperty,
        ClientReportSection::GeneratedContent,
        "Remote generation requires equality with the stored immutable pin.",
    ),
    policy(
        "policy/rust-policy/client-single-bound-module",
        "3.4,3.13",
        ClientAuthority::Engine,
        ClientImplementationSubject::SchemaCompiler,
        ClientEvidenceDomain::SchemaProperty,
        ClientReportSection::GeneratedContent,
        "One client contains Core plus at most one selected module.",
    ),
    policy(
        "policy/rust-policy/client-transitive-dependency-exclusion",
        "3.4,3.13",
        ClientAuthority::Engine,
        ClientImplementationSubject::SchemaCompiler,
        ClientEvidenceDomain::SchemaProperty,
        ClientReportSection::GeneratedContent,
        "Dependencies require independently bound clients rather than merged surfaces.",
    ),
    policy(
        "policy/rust-policy/client-visible-schema-closure",
        "3.5-3.12",
        ClientAuthority::Engine,
        ClientImplementationSubject::SchemaCompiler,
        ClientEvidenceDomain::SchemaProperty,
        ClientReportSection::GeneratedContent,
        "Every extension coordinate must be reachable from the selected module root.",
    ),
    policy(
        "policy/rust-policy/client-core-runtime-reuse",
        "4.1-4.3",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::CoreComposition,
        ClientEvidenceDomain::GeneratedApiProperty,
        ClientReportSection::GeneratedContent,
        "Generated clients reuse the exact public Core and runtime by identity.",
    ),
    policy(
        "policy/rust-policy/client-module-root-composition",
        "4.4",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::ModuleApi,
        ClientEvidenceDomain::GeneratedApiProperty,
        ClientReportSection::GeneratedContent,
        "A local extension trait enters the namespaced module root on the shared client.",
    ),
    policy(
        "policy/rust-policy/client-module-surface-closure",
        "4.5-4.12",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::ModuleApi,
        ClientEvidenceDomain::GeneratedApiProperty,
        ClientReportSection::GeneratedContent,
        "Every reachable module coordinate receives one typed binding.",
    ),
    policy(
        "policy/rust-policy/client-rust-namespace-isolation",
        "4.13-4.17",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::ModuleApi,
        ClientEvidenceDomain::GeneratedApiProperty,
        ClientReportSection::GeneratedContent,
        "Module names cannot shadow Core or reserved generated roles.",
    ),
    policy(
        "policy/rust-policy/client-cargo-project-adoption",
        "5.1-5.17",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::ProjectReconciliation,
        ClientEvidenceDomain::ProjectProperty,
        ClientReportSection::CargoIntegration,
        "Cargo adoption preserves unrelated caller policy and authored bytes.",
    ),
    policy(
        "policy/rust-policy/client-immutable-sdk-dependency",
        "5.5-5.13",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::ProjectReconciliation,
        ClientEvidenceDomain::ProjectProperty,
        ClientReportSection::CargoIntegration,
        "The public SDK dependency is exact and never workspace-local or moving.",
    ),
    policy(
        "policy/rust-policy/client-generated-ownership-manifest",
        "6.1-6.8",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::Publication,
        ClientEvidenceDomain::PublicationProperty,
        ClientReportSection::Regeneration,
        "The manifest is the sole authority for generated replacement and removal.",
    ),
    policy(
        "policy/rust-policy/client-user-file-preservation",
        "6.4-6.15",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::Publication,
        ClientEvidenceDomain::PublicationProperty,
        ClientReportSection::Regeneration,
        "Unknown and authored content survives generation byte-for-byte.",
    ),
    policy(
        "policy/rust-policy/client-obsolete-artifact-removal",
        "6.4-6.8",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::Publication,
        ClientEvidenceDomain::PublicationProperty,
        ClientReportSection::Regeneration,
        "Only previously authenticated obsolete artifacts may be removed.",
    ),
    policy(
        "policy/rust-policy/client-deterministic-regeneration",
        "6.1-6.15",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::Publication,
        ClientEvidenceDomain::PublicationProperty,
        ClientReportSection::Regeneration,
        "Equal semantic inputs produce equal generated bytes and ownership.",
    ),
    policy(
        "policy/rust-policy/client-workspace-cwd-scoping",
        "7.1-7.3",
        ClientAuthority::Engine,
        ClientImplementationSubject::WorkspaceSelection,
        ClientEvidenceDomain::WorkspaceProperty,
        ClientReportSection::Regeneration,
        "Generation selects only managed clients at or below workspace cwd.",
    ),
    policy(
        "policy/rust-policy/client-multi-client-isolation",
        "7.4-7.14",
        ClientAuthority::Engine,
        ClientImplementationSubject::WorkspaceSelection,
        ClientEvidenceDomain::WorkspaceProperty,
        ClientReportSection::Regeneration,
        "Each client keeps independent module, schema, output, and changeset identity.",
    ),
    policy(
        "policy/rust-policy/client-query-usability",
        "9.1-9.14",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::QueryRuntime,
        ClientEvidenceDomain::QueryTransportProperty,
        ClientReportSection::QueryUsability,
        "Generated Core and module queries use the public shared transport contract.",
    ),
    policy(
        "policy/rust-policy/client-diagnostic-and-secret-safety",
        "8.1-8.14",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::DiagnosticSecurity,
        ClientEvidenceDomain::DiagnosticSecurity,
        ClientReportSection::LocalClosure,
        "Diagnostics and generated output retain no credentials or ambient host identity.",
    ),
    policy(
        "policy/rust-policy/client-engine-free-local-checkpoint",
        "10.1-10.12",
        ClientAuthority::RustPolicy,
        ClientImplementationSubject::EvidenceBoundary,
        ClientEvidenceDomain::ImplementationClosure,
        ClientReportSection::LocalClosure,
        "Local closure is Rust-first, scoped, engine-free, and change-triggered.",
    ),
    policy(
        "policy/rust-policy/client-exact-engine-signoff-boundary",
        "10.12-10.19",
        ClientAuthority::Engine,
        ClientImplementationSubject::EvidenceBoundary,
        ClientEvidenceDomain::ExactEngineSignoff,
        ClientReportSection::SdkSignoff,
        "Exact-engine cases consume local closure and share one bounded runtime graph.",
    ),
];

const fn policy(
    id: &'static str,
    requirement: &'static str,
    authority: ClientAuthority,
    subject: ClientImplementationSubject,
    domain: ClientEvidenceDomain,
    section: ClientReportSection,
    rationale: &'static str,
) -> PolicySpec {
    PolicySpec {
        id,
        requirement,
        authority,
        subject,
        domain,
        section,
        rationale,
    }
}
