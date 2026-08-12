//! Exact standalone-client capability scope and evidence admission.
//!
//! This boundary keeps the one retained authority row, the reviewed Rust-policy rows,
//! the ownership-only provision correction, and the engine-hook ownership fence in one
//! closed model. Evidence must match the complete set for its domain before any status
//! change is exposed; a rejection always retains every unresolved blocker.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use dagger_sdk_engine::{
    CheckpointAction, CheckpointActionOutcome, CheckpointGenerationDecision, ClientCheckpointRecord,
};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    CanonicalSet, CapabilityId, Digest, EvidenceId, FeatureId, NonEmptyText, ResolvedLedger,
    Status, TargetDigest,
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
    /// Engine-integration hook-only evidence, never sufficient for client contents.
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

/// Engine-integration boundary row which client evidence may not absorb.
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
    /// Exact hook and operation rows which remain engine-integration owned.
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

    /// Returns engine-integration rows excluded from generated-content evidence.
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

/// Complete local evidence inventory required before standalone-client implementation closure.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientClosureGate {
    /// Direct Rust-owned Go adapter lifecycle fixtures.
    AdapterFixture,
    /// Workspace selection, pin, and multi-client properties.
    WorkspaceProperties,
    /// Visible-schema and compiler properties.
    SchemaCompiler,
    /// Generated public API and compile checks.
    GeneratedApi,
    /// Cargo discovery and semantic reconciliation properties.
    ProjectReconciliation,
    /// Manifest-authorized publication and preservation properties.
    Publication,
    /// Recording-transport Core and module query properties.
    QueryTransport,
    /// Stable diagnostics, source policy, and repository security checks.
    DiagnosticSecurity,
    /// Locked formatting, checking, testing, Clippy, and rustdoc checks.
    CargoHygiene,
    /// Direct engine-free Go ABI tests owned by the Rust adapter.
    DirectGoAbi,
    /// Generated-asset ownership and drift verification.
    GeneratedAssetDrift,
    /// Documentation, command, and derived-report verification.
    DerivedReporting,
    /// Final byte-clean output inspection.
    CleanOutput,
}

/// Terminal result for one implementation-closure gate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ClientClosureGateOutcome {
    /// Gate passed and produced immutable evidence.
    Passed { evidence_digest: Digest },
    /// Gate ran and failed.
    Failed { diagnostic: NonEmptyText },
    /// Gate was not executed.
    Skipped { reason: NonEmptyText },
}

/// Whether a current gate ran now or reused matching immutable prior evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "disposition", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ClientClosureGateDisposition {
    /// Gate executed during this feature-end checkpoint.
    Executed {
        /// Measured gate duration.
        elapsed_millis: u64,
        /// Complete Cargo process count for this gate.
        cargo_invocations: u32,
    },
    /// Matching evidence was consumed without replay.
    Reused {
        /// Canonical identity of the earlier admitted checkpoint evidence.
        prior_checkpoint_digest: Digest,
    },
}

/// One gate observation retained as authored so duplicates remain observable.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientClosureGateObservation {
    /// Closed evidence-domain identity.
    pub gate: ClientClosureGate,
    /// Current owning-input identity required by the gate planner.
    pub expected_input_digest: Digest,
    /// Owning-input identity actually observed by the gate.
    pub observed_input_digest: Digest,
    /// Executed or evidence-reuse accounting.
    pub disposition: ClientClosureGateDisposition,
    /// Passed, failed, or skipped result.
    pub result: ClientClosureGateOutcome,
}

/// Complete engine-free standalone-client closure candidate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientGenerationClosureObservation {
    /// Strict evidence format.
    pub format_version: ClientGenerationFormatVersion,
    /// Exact completeness target.
    pub target_digest: TargetDigest,
    /// Exact standalone-client mapping identity.
    pub mapping_digest: Digest,
    /// Complete Rust implementation identity.
    pub implementation_digest: Digest,
    /// Checked public Core/catalog identity used by generation.
    pub catalog_digest: Digest,
    /// Checked generated-client ownership manifest identity.
    pub manifest_digest: Digest,
    /// Fully accounted typed checkpoint record.
    pub checkpoint: ClientCheckpointRecord,
    /// Materialization count for the fixture SDK dependency baseline.
    pub fixture_baseline_materializations: u32,
    /// Authored gate list; omissions and duplicates are rejected.
    pub gates: Vec<ClientClosureGateObservation>,
    /// Capability-local claims partitioned by reviewed evidence domain.
    pub claims: BTreeMap<ClientEvidenceDomain, CanonicalSet<CapabilityId>>,
}

/// Admitted engine-free implementation closure, distinct from SDK sign-off.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientGenerationClosureEvidence {
    /// Canonical identity of the complete local observation.
    pub closure_digest: Digest,
    /// Exact completeness target.
    pub target_digest: TargetDigest,
    /// Exact standalone-client mapping identity.
    pub mapping_digest: Digest,
    /// Complete Rust implementation identity.
    pub implementation_digest: Digest,
    /// Checked public Core/catalog identity.
    pub catalog_digest: Digest,
    /// Checked generated-client ownership manifest identity.
    pub manifest_digest: Digest,
    /// Status changes supported by complete local evidence.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Exact-engine blockers intentionally retained after local closure.
    pub signoff_blockers: CanonicalSet<CapabilityId>,
}

/// Change-triggered selection of current versus scheduled closure gates.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientFeatureEndGatePlan {
    /// Complete current owning-input identities.
    pub current_inputs: BTreeMap<ClientClosureGate, Digest>,
    /// Current passed observations reusable without replay.
    pub reused: BTreeMap<ClientClosureGate, ClientClosureGateObservation>,
    /// Missing, failed, skipped, or stale gates which must execute.
    pub scheduled: CanonicalSet<ClientClosureGate>,
}

/// Canonical standalone-client closure artifact written by the recorder.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientGenerationEvidenceArtifact {
    /// Strict evidence format.
    pub format_version: ClientGenerationFormatVersion,
    /// Complete authored observation retained for audit.
    pub observation: ClientGenerationClosureObservation,
    /// Admitted local closure.
    pub closure: ClientGenerationClosureEvidence,
    /// Exact deferred case identities; no case outcome is synthesized locally.
    pub deferred_signoff_cases: CanonicalSet<ClientSignoffCase>,
}

/// One exact engine-backed standalone-client sign-off case.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientSignoffCase {
    /// Initialize and generate one confined local client.
    InitializedLocalClient,
    /// Generate one independently bound immutable remote dependency client.
    PinnedRemoteClient,
    /// Regenerate while preserving authored content and removing only owned obsolete files.
    SchemaRegeneration,
    /// Execute a generated Core query through the public runtime.
    CoreQuery,
    /// Execute a query through the selected module namespace.
    NamespacedModuleQuery,
}

/// Immutable exact-target artifact inputs shared by every client sign-off case.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffArtifactInput {
    /// Exact completeness target.
    pub target_digest: TargetDigest,
    /// Target platform identity.
    pub platform: NonEmptyText,
    /// Combined engine and CLI immutable input identity.
    pub engine_cli_input_digest: Digest,
    /// Mandatory engine-packaged Go runtime identity.
    pub go_runtime_digest: Digest,
    /// Exact Rust SDK manifest identity.
    pub rust_manifest_digest: Digest,
    /// Exact Rust engine descriptor identity.
    pub rust_descriptor_digest: Digest,
    /// Rust generated assets and source content identity.
    pub rust_content_digest: Digest,
    /// Exact Rust and Go toolchain identity.
    pub toolchain_digest: Digest,
}

/// One content-addressed exact-target artifact reused throughout sign-off.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffArtifact {
    /// Immutable artifact inputs.
    pub input: ClientSignoffArtifactInput,
    /// Domain-separated canonical artifact identity.
    pub artifact_digest: Digest,
}

/// One isolated case bound to the shared artifact and local closure.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffCaseSpec {
    /// Stable case identity.
    pub case: ClientSignoffCase,
    /// Artifact-, closure-, and case-bound identity.
    pub case_digest: Digest,
    /// Exact engine-domain claims assigned to this case.
    pub capability_ids: CanonicalSet<CapabilityId>,
}

/// Complete deferred standalone-client sign-off inventory.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffInventory {
    /// Strict evidence format.
    pub format_version: ClientGenerationFormatVersion,
    /// Exact standalone-client mapping identity.
    pub mapping_digest: Digest,
    /// Matching local closure consumed without replay.
    pub implementation_closure_digest: Digest,
    /// One reusable exact-target artifact.
    pub artifact: ClientSignoffArtifact,
    /// One installed Rust baseline shared by isolated workspaces.
    pub rust_baseline_digest: Digest,
    /// Complete closed five-case inventory.
    pub cases: BTreeMap<ClientSignoffCase, ClientSignoffCaseSpec>,
}

/// Terminal state of one engine-backed client case.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ClientSignoffCaseOutcome {
    /// Case passed with immutable evidence.
    Passed { observation_digest: Digest },
    /// Case ran and failed.
    Failed { diagnostic: NonEmptyText },
    /// Case did not execute.
    Skipped { reason: NonEmptyText },
}

/// One isolated case outcome branched from the common installed baseline.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffCaseObservation {
    /// Stable case identity.
    pub case: ClientSignoffCase,
    /// Expected case digest from the inventory.
    pub case_digest: Digest,
    /// Unique isolated workspace identity.
    pub workspace_digest: Digest,
    /// Measured case duration.
    pub elapsed_millis: u64,
    /// Passed, failed, or skipped result.
    pub result: ClientSignoffCaseOutcome,
}

/// Counts proving reuse of every expensive sign-off resource.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffExecutionCounts {
    /// Target artifact build or import count.
    pub artifact_materializations: u32,
    /// Engine binary build count; zero is valid when the artifact was imported.
    pub engine_builds: u32,
    /// CLI binary build count; zero is valid when the artifact was imported.
    pub cli_builds: u32,
    /// Mandatory Go runtime content build count.
    pub go_runtime_builds: u32,
    /// Rust content build count.
    pub rust_content_builds: u32,
    /// Engine service start count.
    pub engine_starts: u32,
    /// Installed Rust baseline materialization count.
    pub rust_baseline_installs: u32,
    /// Engine-free closure replay count; this must remain zero.
    pub implementation_closure_replays: u32,
    /// Unrelated SDK, generation, test, or distribution graph entries.
    pub unrelated_actions: u32,
}

/// Shared expensive-phase timings for exact-target sign-off.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffPhaseTimings {
    /// Artifact build or import duration.
    pub artifact_build_or_import_millis: u64,
    /// One engine startup duration.
    pub engine_start_millis: u64,
    /// One Rust baseline installation duration.
    pub rust_install_millis: u64,
}

/// Complete exact-target run from which the atomic verdict is derived.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffRun {
    /// Canonical inventory identity observed by the runner.
    pub inventory_digest: Digest,
    /// Exact shared artifact used by every case.
    pub artifact_digest: Digest,
    /// Matching local closure consumed rather than replayed.
    pub implementation_closure_digest: Digest,
    /// Matching installed Rust baseline.
    pub rust_baseline_digest: Digest,
    /// Expensive resource counts.
    pub execution_counts: ClientSignoffExecutionCounts,
    /// Expensive shared phase timings.
    pub phase_timings: ClientSignoffPhaseTimings,
    /// Authored case outcomes; omissions and duplicates are rejected.
    pub cases: Vec<ClientSignoffCaseObservation>,
}

/// One submitted sign-off result carrying its independently recomputable verdict.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientSignoffObservation {
    /// Complete run data.
    pub run: ClientSignoffRun,
    /// Atomic digest of the complete run data.
    pub verdict_digest: Digest,
}

/// Pure sign-off admission result.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientSignoffAdmission {
    /// Admitted atomic verdict; absent on every rejection.
    pub verdict_digest: Option<Digest>,
    /// Engine-dependent status changes supported by the complete run.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Every remaining blocker.
    pub blockers: CanonicalSet<CapabilityId>,
    /// Stable rejection reason, absent only for admitted sign-off.
    pub rejection: Option<ClientGenerationDiagnosticSet>,
}

/// Independently observable state of one standalone-client evidence phase.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "status", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ClientEvidencePhase {
    /// No admitted evidence exists for this phase.
    Unexecuted,
    /// Complete evidence passed and is bound to this identity.
    Passed { evidence_digest: Digest },
}

/// Honest standalone-client completeness report derived only from admitted evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientGenerationCompletenessReport {
    /// Exact completeness target.
    pub target_digest: TargetDigest,
    /// Reviewed standalone-client mapping identity.
    pub mapping_digest: Digest,
    /// Approved one-client dependency interpretation.
    pub dependency_scope: ClientDependencyScope,
    /// Reviewed ownership-only correction retained in output.
    pub ownership_correction: ClientOwnershipCorrection,
    /// Engine-integration boundaries which client contents never absorb.
    pub preserved_boundaries: Vec<PreservedClientBoundary>,
    /// Engine-free implementation phase.
    pub implementation_closure: ClientEvidencePhase,
    /// Deferred exact-engine phase.
    pub sdk_signoff: ClientEvidencePhase,
    /// Status changes supported by admitted phases.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Remaining blockers partitioned by durable report section.
    pub blockers: BTreeMap<ClientReportSection, CanonicalSet<CapabilityId>>,
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
/// The baseline remains independently reproducible before this transition. It
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

/// Returns the exact engine-free gate set required for standalone-client closure.
#[must_use]
pub fn required_client_closure_gates() -> BTreeSet<ClientClosureGate> {
    BTreeSet::from([
        ClientClosureGate::AdapterFixture,
        ClientClosureGate::WorkspaceProperties,
        ClientClosureGate::SchemaCompiler,
        ClientClosureGate::GeneratedApi,
        ClientClosureGate::ProjectReconciliation,
        ClientClosureGate::Publication,
        ClientClosureGate::QueryTransport,
        ClientClosureGate::DiagnosticSecurity,
        ClientClosureGate::CargoHygiene,
        ClientClosureGate::DirectGoAbi,
        ClientClosureGate::GeneratedAssetDrift,
        ClientClosureGate::DerivedReporting,
        ClientClosureGate::CleanOutput,
    ])
}

/// Plans only missing, failed, skipped, or stale feature-end gates.
pub fn plan_client_feature_end_gate(
    current_inputs: BTreeMap<ClientClosureGate, Digest>,
    retained: &[ClientClosureGateObservation],
) -> Result<ClientFeatureEndGatePlan, ClientGenerationDiagnosticSet> {
    let rejected = || {
        ClientGenerationDiagnosticSet::one(
            ClientGenerationDiagnosticCode::ClientClosureIncomplete,
            "standalone-client feature-end inputs are incomplete or duplicated",
        )
    };
    if BTreeSet::from_iter(current_inputs.keys().copied()) != required_client_closure_gates() {
        return Err(rejected());
    }
    let mut observations = BTreeMap::new();
    for observation in retained {
        if observations
            .insert(observation.gate, observation.clone())
            .is_some()
        {
            return Err(rejected());
        }
    }
    if observations
        .keys()
        .any(|gate| !current_inputs.contains_key(gate))
    {
        return Err(rejected());
    }
    let mut reused = BTreeMap::new();
    let mut scheduled = Vec::new();
    for (gate, current) in &current_inputs {
        match observations.get(gate) {
            Some(observation)
                if observation.expected_input_digest == *current
                    && observation.observed_input_digest == *current
                    && matches!(observation.result, ClientClosureGateOutcome::Passed { .. }) =>
            {
                reused.insert(*gate, observation.clone());
            }
            _ => scheduled.push(*gate),
        }
    }
    Ok(ClientFeatureEndGatePlan {
        current_inputs,
        reused,
        scheduled: CanonicalSet::new(scheduled),
    })
}

/// Returns the exact local capability partition implied by the reviewed mappings.
#[must_use]
pub fn client_implementation_closure_claims(
    scope: &ClientGenerationScope,
) -> BTreeMap<ClientEvidenceDomain, CanonicalSet<CapabilityId>> {
    let mut claims = BTreeMap::<ClientEvidenceDomain, Vec<CapabilityId>>::new();
    for mapping in scope.mappings.values() {
        for domain in &mapping.evidence_domains {
            if *domain != ClientEvidenceDomain::ExactEngineSignoff {
                claims
                    .entry(*domain)
                    .or_default()
                    .push(mapping.capability_id.clone());
            }
        }
    }
    claims
        .into_iter()
        .map(|(domain, ids)| (domain, CanonicalSet::new(ids)))
        .collect()
}

/// Admits local closure only from the complete current engine-free evidence set.
pub fn admit_client_generation_closure(
    scope: &ClientGenerationScope,
    observation: &ClientGenerationClosureObservation,
) -> Result<ClientGenerationClosureEvidence, ClientGenerationDiagnosticSet> {
    let rejected = || {
        ClientGenerationDiagnosticSet::one(
            ClientGenerationDiagnosticCode::ClientClosureIncomplete,
            "standalone-client closure is stale, incomplete, failed, duplicated, or outside the engine-free graph",
        )
    };
    if observation.target_digest != *scope.target_digest()
        || observation.mapping_digest != *scope.mapping_digest()
        || observation.implementation_digest.as_str()
            != observation
                .checkpoint
                .checkpoint
                .implementation_digest
                .as_str()
        || observation.manifest_digest.as_str()
            != observation.checkpoint.asset_output_digest.as_str()
        || observation.fixture_baseline_materializations != 1
        || observation
            .checkpoint
            .checkpoint
            .deferred_signoff_exception
            .is_some()
        || observation
            .checkpoint
            .checkpoint
            .actions
            .iter()
            .any(|action| {
                action.elapsed_millis == 0 || action.outcome != CheckpointActionOutcome::Passed
            })
    {
        return Err(rejected());
    }
    let checkpoint_actions = observation
        .checkpoint
        .checkpoint
        .actions
        .iter()
        .map(|item| item.action.clone())
        .collect::<BTreeSet<_>>();
    let cargo_actions = observation
        .checkpoint
        .cargo
        .iter()
        .map(|item| item.action.clone())
        .collect::<BTreeSet<_>>();
    let generation_manifest = match &observation.checkpoint.checkpoint.generation {
        CheckpointGenerationDecision::ReuseChecked { manifest_digest }
        | CheckpointGenerationDecision::ScopedRefresh {
            manifest_digest, ..
        } => manifest_digest,
    };
    if checkpoint_actions.len() != observation.checkpoint.checkpoint.actions.len()
        || cargo_actions.len() != observation.checkpoint.cargo.len()
        || checkpoint_actions != cargo_actions
        || generation_manifest.as_str() != observation.manifest_digest.as_str()
        || observation
            .checkpoint
            .cargo
            .iter()
            .any(|item| !client_closure_cargo_count_is_valid(&item.action, item.invocations))
    {
        return Err(rejected());
    }

    let mut gates = BTreeMap::new();
    for gate in &observation.gates {
        if gate.expected_input_digest != gate.observed_input_digest
            || matches!(
                gate.disposition,
                ClientClosureGateDisposition::Executed {
                    elapsed_millis: 0,
                    ..
                }
            )
            || gates.insert(gate.gate, &gate.result).is_some()
        {
            return Err(rejected());
        }
    }
    if BTreeSet::from_iter(gates.keys().copied()) != required_client_closure_gates()
        || gates
            .values()
            .any(|result| !matches!(result, ClientClosureGateOutcome::Passed { .. }))
    {
        return Err(rejected());
    }

    let expected_claims = client_implementation_closure_claims(scope);
    if observation.claims != expected_claims
        || observation
            .claims
            .contains_key(&ClientEvidenceDomain::ExactEngineSignoff)
        || observation
            .claims
            .contains_key(&ClientEvidenceDomain::EngineHook)
    {
        return Err(rejected());
    }

    let closure_digest =
        canonical_digest(DigestDomain::ClientGeneration, observation).map_err(|_| rejected())?;
    let mut status_changes = BTreeMap::new();
    for (domain, capability_ids) in &observation.claims {
        let admission = admit_client_evidence(
            scope,
            &ClientEvidenceObservation {
                format_version: ClientGenerationFormatVersion::current(),
                evidence_id: EvidenceId::new(format!(
                    "verification/client-generation/implementation-closure/{}",
                    client_evidence_domain_slug(*domain)
                ))
                .expect("closed evidence domain produces a valid identity"),
                target_digest: observation.target_digest.clone(),
                mapping_digest: observation.mapping_digest.clone(),
                domain: *domain,
                capability_ids: capability_ids.iter().cloned().collect(),
                result: ClientEvidenceOutcome::Passed {
                    observation_digest: closure_digest.clone(),
                },
            },
        );
        if admission.rejection.is_some() {
            return Err(rejected());
        }
        status_changes.extend(admission.status_changes);
    }
    let signoff_blockers = CanonicalSet::new(
        scope
            .blockers()
            .into_iter()
            .filter(|id| !status_changes.contains_key(id)),
    );
    Ok(ClientGenerationClosureEvidence {
        closure_digest,
        target_digest: observation.target_digest.clone(),
        mapping_digest: observation.mapping_digest.clone(),
        implementation_digest: observation.implementation_digest.clone(),
        catalog_digest: observation.catalog_digest.clone(),
        manifest_digest: observation.manifest_digest.clone(),
        status_changes,
        signoff_blockers,
    })
}

/// Builds one immutable exact-target client sign-off artifact without engine work.
pub fn build_client_signoff_artifact(
    input: ClientSignoffArtifactInput,
) -> Result<ClientSignoffArtifact, ClientGenerationDiagnosticSet> {
    let artifact_digest =
        canonical_digest(DigestDomain::ClientGeneration, &input).map_err(|_| {
            ClientGenerationDiagnosticSet::one(
                ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
                "standalone-client sign-off artifact could not be hashed",
            )
        })?;
    Ok(ClientSignoffArtifact {
        input,
        artifact_digest,
    })
}

/// Returns the complete closed deferred client case inventory.
#[must_use]
pub fn required_client_signoff_cases() -> BTreeSet<ClientSignoffCase> {
    BTreeSet::from([
        ClientSignoffCase::InitializedLocalClient,
        ClientSignoffCase::PinnedRemoteClient,
        ClientSignoffCase::SchemaRegeneration,
        ClientSignoffCase::CoreQuery,
        ClientSignoffCase::NamespacedModuleQuery,
    ])
}

/// Constructs the deferred five-case inventory without starting an engine.
pub fn build_client_signoff_inventory(
    scope: &ClientGenerationScope,
    closure: &ClientGenerationClosureEvidence,
    artifact: ClientSignoffArtifact,
    rust_baseline_digest: Digest,
) -> Result<ClientSignoffInventory, ClientGenerationDiagnosticSet> {
    let rejected = || {
        ClientGenerationDiagnosticSet::one(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off inventory combines stale or incomplete identities",
        )
    };
    if closure.target_digest != *scope.target_digest()
        || closure.mapping_digest != *scope.mapping_digest()
        || closure.target_digest != artifact.input.target_digest
        || build_client_signoff_artifact(artifact.input.clone())? != artifact
        || closure.signoff_blockers != exact_client_signoff_claims(scope)
    {
        return Err(rejected());
    }
    let signoff_claims = exact_client_signoff_claims(scope);
    let mut cases = BTreeMap::new();
    for case in required_client_signoff_cases() {
        // Initialization is the engine-owned lifecycle boundary. The remaining cases
        // still participate in the atomic verdict but do not claim that row alone.
        let capability_ids = if case == ClientSignoffCase::InitializedLocalClient {
            signoff_claims.clone()
        } else {
            CanonicalSet::default()
        };
        let case_digest = canonical_digest(
            DigestDomain::ClientGeneration,
            &(
                case,
                &artifact.artifact_digest,
                &closure.closure_digest,
                &rust_baseline_digest,
                &capability_ids,
            ),
        )
        .map_err(|_| rejected())?;
        cases.insert(
            case,
            ClientSignoffCaseSpec {
                case,
                case_digest,
                capability_ids,
            },
        );
    }
    Ok(ClientSignoffInventory {
        format_version: ClientGenerationFormatVersion::current(),
        mapping_digest: scope.mapping_digest().clone(),
        implementation_closure_digest: closure.closure_digest.clone(),
        artifact,
        rust_baseline_digest,
        cases,
    })
}

/// Computes the atomic verdict expected for one complete exact-target run.
pub fn client_signoff_verdict_digest(
    run: &ClientSignoffRun,
) -> Result<Digest, ClientGenerationDiagnosticSet> {
    canonical_digest(DigestDomain::ClientGeneration, run).map_err(|_| {
        ClientGenerationDiagnosticSet::one(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off verdict could not be hashed",
        )
    })
}

/// Atomically validates the bounded exact-target sign-off candidate.
#[must_use]
pub fn validate_client_signoff_candidate(
    scope: &ClientGenerationScope,
    closure: &ClientGenerationClosureEvidence,
    inventory: &ClientSignoffInventory,
    observation: &ClientSignoffObservation,
) -> ClientSignoffAdmission {
    let reject = |code, message| ClientSignoffAdmission {
        verdict_digest: None,
        status_changes: BTreeMap::new(),
        // A rejected engine run cannot erase the independently admitted local
        // closure; it retains only that closure's exact residual blockers.
        blockers: closure.signoff_blockers.clone(),
        rejection: Some(ClientGenerationDiagnosticSet::one(code, message)),
    };
    let Ok(expected_inventory_digest) = canonical_digest(DigestDomain::ClientGeneration, inventory)
    else {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off inventory could not be hashed",
        );
    };
    let Ok(expected_artifact) = build_client_signoff_artifact(inventory.artifact.input.clone())
    else {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off artifact is not canonical",
        );
    };
    if closure.target_digest != *scope.target_digest()
        || closure.mapping_digest != *scope.mapping_digest()
        || inventory.mapping_digest != *scope.mapping_digest()
        || inventory.implementation_closure_digest != closure.closure_digest
        || inventory.artifact != expected_artifact
        || inventory.artifact.input.target_digest != *scope.target_digest()
        || observation.run.inventory_digest != expected_inventory_digest
        || observation.run.artifact_digest != inventory.artifact.artifact_digest
        || observation.run.implementation_closure_digest != closure.closure_digest
        || observation.run.rust_baseline_digest != inventory.rust_baseline_digest
    {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off is stale or cross-target",
        );
    }

    let counts = observation.run.execution_counts;
    if counts.artifact_materializations != 1
        || counts.engine_builds > 1
        || counts.cli_builds > 1
        || counts.go_runtime_builds > 1
        || counts.rust_content_builds > 1
        || counts.engine_starts != 1
        || counts.rust_baseline_installs != 1
        || counts.implementation_closure_replays != 0
        || counts.unrelated_actions != 0
    {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffDuplicateWork,
            "standalone-client sign-off did not reuse one bounded artifact, engine, and Rust baseline",
        );
    }
    let timings = observation.run.phase_timings;
    if timings.artifact_build_or_import_millis == 0
        || timings.engine_start_millis == 0
        || timings.rust_install_millis == 0
    {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off omitted a shared phase timing",
        );
    }

    let mut cases = BTreeMap::new();
    let mut workspaces = BTreeSet::new();
    for case in &observation.run.cases {
        let Some(spec) = inventory.cases.get(&case.case) else {
            return reject(
                ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
                "standalone-client sign-off contains an unknown case",
            );
        };
        if case.case_digest != spec.case_digest
            || case.elapsed_millis == 0
            || !matches!(case.result, ClientSignoffCaseOutcome::Passed { .. })
            || !workspaces.insert(case.workspace_digest.clone())
            || cases.insert(case.case, case).is_some()
        {
            return reject(
                ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
                "standalone-client sign-off case is stale, shared, duplicated, skipped, or failed",
            );
        }
    }
    if BTreeSet::from_iter(cases.keys().copied()) != required_client_signoff_cases()
        || CanonicalSet::new(
            inventory
                .cases
                .values()
                .flat_map(|case| case.capability_ids.iter().cloned()),
        ) != exact_client_signoff_claims(scope)
    {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off omits a required case or claim",
        );
    }
    let Ok(verdict_digest) = client_signoff_verdict_digest(&observation.run) else {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off verdict could not be hashed",
        );
    };
    if verdict_digest != observation.verdict_digest {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off verdict is not atomic",
        );
    }

    let admission = admit_client_evidence(
        scope,
        &ClientEvidenceObservation {
            format_version: ClientGenerationFormatVersion::current(),
            evidence_id: EvidenceId::new("verification/client-generation/sdk-signoff")
                .expect("static evidence identity is valid"),
            target_digest: scope.target_digest().clone(),
            mapping_digest: scope.mapping_digest().clone(),
            domain: ClientEvidenceDomain::ExactEngineSignoff,
            capability_ids: exact_client_signoff_claims(scope).iter().cloned().collect(),
            result: ClientEvidenceOutcome::Passed {
                observation_digest: verdict_digest.clone(),
            },
        },
    );
    if admission.rejection.is_some() {
        return reject(
            ClientGenerationDiagnosticCode::ClientSignoffIncomplete,
            "standalone-client sign-off claims failed capability-local admission",
        );
    }
    ClientSignoffAdmission {
        verdict_digest: Some(verdict_digest),
        status_changes: admission.status_changes,
        blockers: CanonicalSet::default(),
        rejection: None,
    }
}

/// Derives the honest standalone-client report from independently admitted phases.
pub fn derive_client_generation_report(
    scope: &ClientGenerationScope,
    closure: Option<&ClientGenerationClosureEvidence>,
    signoff: Option<&ClientSignoffAdmission>,
) -> Result<ClientGenerationCompletenessReport, ClientGenerationDiagnosticSet> {
    let rejected = || {
        ClientGenerationDiagnosticSet::one(
            ClientGenerationDiagnosticCode::CapabilityEvidenceIncomplete,
            "standalone-client report input is stale, rejected, or incomplete",
        )
    };
    if signoff.is_some() && closure.is_none() {
        return Err(rejected());
    }

    let expected_local = expected_client_status_changes(scope, |mapping| {
        !mapping
            .evidence_domains
            .contains(&ClientEvidenceDomain::ExactEngineSignoff)
    });
    let expected_signoff = expected_client_status_changes(scope, |mapping| {
        mapping
            .evidence_domains
            .contains(&ClientEvidenceDomain::ExactEngineSignoff)
    });
    let mut status_changes = BTreeMap::new();
    let implementation_closure = if let Some(closure) = closure {
        if closure.target_digest != *scope.target_digest()
            || closure.mapping_digest != *scope.mapping_digest()
            || closure.status_changes != expected_local
            || closure.signoff_blockers != exact_client_signoff_claims(scope)
        {
            return Err(rejected());
        }
        status_changes.extend(closure.status_changes.clone());
        ClientEvidencePhase::Passed {
            evidence_digest: closure.closure_digest.clone(),
        }
    } else {
        ClientEvidencePhase::Unexecuted
    };
    let sdk_signoff = if let Some(signoff) = signoff {
        let Some(verdict_digest) = &signoff.verdict_digest else {
            return Err(rejected());
        };
        if signoff.rejection.is_some()
            || signoff.status_changes != expected_signoff
            || !signoff.blockers.is_empty()
        {
            return Err(rejected());
        }
        status_changes.extend(signoff.status_changes.clone());
        ClientEvidencePhase::Passed {
            evidence_digest: verdict_digest.clone(),
        }
    } else {
        ClientEvidencePhase::Unexecuted
    };

    let proved = status_changes.keys().cloned().collect::<BTreeSet<_>>();
    let raw = report(scope, &proved);
    Ok(ClientGenerationCompletenessReport {
        target_digest: scope.target_digest().clone(),
        mapping_digest: scope.mapping_digest().clone(),
        dependency_scope: ClientDependencyScope::CorePlusOneBoundModule,
        ownership_correction: scope.ownership_correction().clone(),
        preserved_boundaries: scope.preserved_boundaries().values().cloned().collect(),
        implementation_closure,
        sdk_signoff,
        status_changes,
        blockers: raw
            .blockers
            .into_iter()
            .map(|(section, blockers)| (section, CanonicalSet::new(blockers)))
            .collect(),
    })
}

fn exact_client_signoff_claims(scope: &ClientGenerationScope) -> CanonicalSet<CapabilityId> {
    CanonicalSet::new(
        scope
            .mappings
            .values()
            .filter(|mapping| {
                mapping
                    .evidence_domains
                    .contains(&ClientEvidenceDomain::ExactEngineSignoff)
            })
            .map(|mapping| mapping.capability_id.clone()),
    )
}

fn expected_client_status_changes(
    scope: &ClientGenerationScope,
    predicate: impl Fn(&ClientGenerationMapping) -> bool,
) -> BTreeMap<CapabilityId, Status> {
    scope
        .mappings
        .values()
        .filter(|mapping| predicate(mapping))
        .map(|mapping| {
            let status = match mapping.allowed_terminal_status {
                ClientTerminalStatus::Implemented => Status::Implemented,
                ClientTerminalStatus::IdiomaticEquivalent => Status::IdiomaticEquivalent,
            };
            (mapping.capability_id.clone(), status)
        })
        .collect()
}

const fn client_evidence_domain_slug(domain: ClientEvidenceDomain) -> &'static str {
    match domain {
        ClientEvidenceDomain::AdapterFixture => "adapter-fixture",
        ClientEvidenceDomain::WorkspaceProperty => "workspace-property",
        ClientEvidenceDomain::SchemaProperty => "schema-property",
        ClientEvidenceDomain::GeneratedApiProperty => "generated-api-property",
        ClientEvidenceDomain::ProjectProperty => "project-property",
        ClientEvidenceDomain::PublicationProperty => "publication-property",
        ClientEvidenceDomain::QueryTransportProperty => "query-transport-property",
        ClientEvidenceDomain::DiagnosticSecurity => "diagnostic-security",
        ClientEvidenceDomain::ImplementationClosure => "implementation-closure",
        ClientEvidenceDomain::ExactEngineSignoff => "exact-engine-signoff",
        ClientEvidenceDomain::EngineHook => "engine-hook",
    }
}

const fn client_closure_cargo_count_is_valid(action: &CheckpointAction, count: u32) -> bool {
    match action {
        CheckpointAction::DirectGoAbi { .. } | CheckpointAction::CleanOutput => count == 0,
        CheckpointAction::Format { .. }
        | CheckpointAction::Check { .. }
        | CheckpointAction::Test { .. }
        | CheckpointAction::Clippy { .. }
        | CheckpointAction::Rustdoc { .. }
        | CheckpointAction::CargoDeny
        | CheckpointAction::RepositoryRustSecurity
        | CheckpointAction::GeneratedAssetDrift
        | CheckpointAction::PackageContents { .. } => count > 0,
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
