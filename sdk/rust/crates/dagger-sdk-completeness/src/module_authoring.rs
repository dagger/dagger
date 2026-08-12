//! Exact module-authoring scope, ownership correction, and evidence admission.
//!
//! This module is the completeness boundary rather than an implementation shortcut.
//! It retains every reviewed authority row and Rust-policy row as a closed set, routes
//! lifecycle-only harness rows away from authoring, and rejects an observation before
//! producing any status change when target, scope, outcome, or evidence domain differs.

use std::collections::{BTreeMap, BTreeSet};

use dagger_sdk_engine::{CheckpointActionOutcome, CheckpointGenerationDecision, CheckpointRecord};
use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    CanonicalSet, CapabilityId, Digest, EvidenceId, FeatureId, NonEmptyText, Status, TargetDigest,
};

const MODULE_AUTHORING_EXISTING_SCOPE_DIGEST: &str =
    "sha256:2e78e144a19072d7e85483d7496b987904c91f99f2b9f7e567af2f4b6163b7a9";

/// Strict module-authoring scope/evidence wire format.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ModuleAuthoringFormatVersion(u32);

impl ModuleAuthoringFormatVersion {
    /// Returns the only accepted scope/evidence format.
    #[must_use]
    pub const fn current() -> Self {
        Self(1)
    }
}

impl Serialize for ModuleAuthoringFormatVersion {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: serde::Serializer,
    {
        serializer.serialize_u32(self.0)
    }
}

impl<'de> Deserialize<'de> for ModuleAuthoringFormatVersion {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let value = u32::deserialize(deserializer)?;
        if value == 1 {
            Ok(Self(value))
        } else {
            Err(serde::de::Error::custom(
                "unsupported module authoring format version",
            ))
        }
    }
}

/// Authority owning the observable capability represented by a mapping.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleAuthority {
    /// Definitive Go module generator evidence.
    GoCodegen,
    /// Definitive Go module-global client evidence.
    GoClient,
    /// Reviewed Rust-native policy.
    RustPolicy,
}

/// Closed implementation subject responsible for one mapped capability.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleImplementationSubject {
    /// Procedural attributes and typed crate-local bridges.
    AuthoringBridge,
    /// Immutable source snapshot and source compiler.
    SourceCompiler,
    /// Type, metadata, descriptor, registration, and introspection projection.
    TypeProjection,
    /// Parent/argument codecs and production dispatch state machine.
    DispatchRuntime,
    /// Active-session module context and definitive helper mappings.
    ModuleContext,
    /// Generated ownership and scoped regeneration.
    GeneratedAssets,
    /// Scope, local closure, and exact-engine sign-off separation.
    EvidenceBoundary,
}

/// Finite observation domain permitted to prove a module capability.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleEvidenceDomain {
    /// Procedural/source compile fixture.
    CompileFixture,
    /// Pure source/compiler property.
    CompilerProperty,
    /// Engine-independent production dispatcher property.
    DispatchProperty,
    /// Generated ownership and regeneration property.
    AssetProperty,
    /// Warning, documentation, package, and security hygiene.
    SecurityHygiene,
    /// Exact-target engine sign-off.
    ExactEngineSignoff,
    /// Sibling standalone-client work, never admitted here.
    SiblingStandaloneClient,
    /// Feature 5 lifecycle-only engine integration, never authoring evidence.
    LifecycleIntegration,
    /// Cross-platform release evidence, deferred from local closure.
    CrossPlatform,
}

/// Terminal status one complete mapping is allowed to request.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleTerminalStatus {
    /// Direct complete implementation.
    Implemented,
    /// Complete Rust-native behavioural equivalent.
    IdiomaticEquivalent,
    /// Reviewed absence of a sound Rust analogue.
    Inapplicable,
}

/// One exact capability-to-implementation mapping.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleAuthoringMapping {
    /// Stable capability identity.
    pub capability_id: CapabilityId,
    /// Owning authority.
    pub authority: ModuleAuthority,
    /// One approved acceptance-criterion coordinate.
    pub requirement: NonEmptyText,
    /// Concrete Rust implementation subject.
    pub implementation_subject: ModuleImplementationSubject,
    /// Reviewed behavioural rationale.
    pub rationale: NonEmptyText,
    /// Only final status this mapping may request.
    pub allowed_terminal_status: ModuleTerminalStatus,
    /// Smallest observation domain capable of proving the row.
    pub minimum_evidence_domain: ModuleEvidenceDomain,
    /// Exact target to which the mapping applies.
    pub target_digest: TargetDigest,
    /// Whether an unproved row must remain a rendered blocker.
    pub blocker: bool,
}

/// Routing-only correction for one lifecycle capability.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OwnershipCorrection {
    /// Lifecycle capability removed from authoring scope.
    pub capability_id: CapabilityId,
    /// Prior coarse owner.
    pub from: FeatureId,
    /// Correct engine-integration owner.
    pub to: FeatureId,
}

/// Authored scope input retained as lists so duplicate rows remain observable.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleAuthoringScopeInput {
    /// Strict mapping format version.
    pub format_version: ModuleAuthoringFormatVersion,
    /// Reviewed digest of the retained 79 authority rows.
    pub existing_scope_digest: Digest,
    /// Exact target shared by all mappings.
    pub target_digest: TargetDigest,
    /// Retained authority and added Rust-policy rows.
    pub mappings: Vec<ModuleAuthoringMapping>,
    /// Exact 17-row lifecycle ownership correction.
    pub ownership_corrections: Vec<OwnershipCorrection>,
}

/// Duplicate-free exact module-authoring scope safe for evidence admission.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleAuthoringScope {
    target_digest: TargetDigest,
    mapping_digest: Digest,
    mappings: BTreeMap<CapabilityId, ModuleAuthoringMapping>,
    ownership_corrections: BTreeMap<CapabilityId, OwnershipCorrection>,
}

impl ModuleAuthoringScope {
    /// Returns the exact target bound to the complete mapping.
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
    pub const fn mappings(&self) -> &BTreeMap<CapabilityId, ModuleAuthoringMapping> {
        &self.mappings
    }

    /// Returns every corrected lifecycle row in canonical identity order.
    #[must_use]
    pub const fn ownership_corrections(&self) -> &BTreeMap<CapabilityId, OwnershipCorrection> {
        &self.ownership_corrections
    }

    /// Returns all currently unproved blocking identities.
    pub fn blockers(&self) -> CanonicalSet<CapabilityId> {
        CanonicalSet::new(
            self.mappings
                .values()
                .filter(|mapping| mapping.blocker)
                .map(|mapping| mapping.capability_id.clone()),
        )
    }
}

/// Evidence outcome; only `Passed` can request a status transition.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ModuleEvidenceOutcome {
    /// Observation completed successfully.
    Passed { observation_digest: Digest },
    /// Observation ran and failed.
    Failed { diagnostic: NonEmptyText },
    /// Observation did not execute.
    Skipped { reason: NonEmptyText },
}

/// Strict target- and scope-bound module evidence observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleEvidenceObservation {
    /// Strict observation format version.
    pub format_version: ModuleAuthoringFormatVersion,
    /// Stable evidence identity.
    pub evidence_id: EvidenceId,
    /// Exact checked target.
    pub target_digest: TargetDigest,
    /// Complete mapping identity observed by the producer.
    pub mapping_digest: Digest,
    /// Finite observation domain.
    pub domain: ModuleEvidenceDomain,
    /// Exact capability claims made by this observation.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Pass/fail/skip outcome.
    pub result: ModuleEvidenceOutcome,
}

/// Admission result that makes rejected no-op behaviour explicit.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleAuthoringEvidenceAdmission {
    /// Permitted status changes; empty for every rejected observation.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Every unclosed blocker after considering this one observation.
    pub blockers: CanonicalSet<CapabilityId>,
    /// Stable rejection reason, absent only for admitted evidence.
    pub rejection: Option<NonEmptyText>,
}

/// Complete local gate inventory required for implementation closure.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ImplementationClosureGate {
    /// Production authoring compiler properties.
    CompilerProperties,
    /// Production dispatcher and context properties.
    DispatcherProperties,
    /// Bounded compile-pass and compile-fail fixtures.
    CompileFixtures,
    /// Locked workspace formatting check.
    Format,
    /// Locked workspace type check.
    Check,
    /// Locked workspace test inventory.
    Test,
    /// Warning-denied Clippy.
    Clippy,
    /// Warning-denied rustdoc.
    Rustdoc,
    /// Cargo Deny dependency and license policy.
    CargoDeny,
    /// Repository Rust security command set.
    RepositoryRustSecurity,
    /// Checked generated-asset drift decision.
    GeneratedAssetDrift,
    /// Manifest-authorized generated ownership.
    GeneratedAssetOwnership,
    /// Exact two-package public graph and package contents.
    PublicPackagePolicy,
    /// Direct Rust-owned Go ABI tests.
    DirectGoAbi,
    /// Derived completeness output verification.
    DerivedReporting,
    /// Byte-clean generated and source output inspection.
    CleanOutput,
}

/// Terminal state for one implementation-closure gate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ImplementationGateOutcome {
    /// Gate completed successfully with immutable evidence.
    Passed { evidence_digest: Digest },
    /// Gate ran and failed.
    Failed { diagnostic: NonEmptyText },
    /// Gate did not execute.
    Skipped { reason: NonEmptyText },
}

/// One authored gate observation retained before exact-set admission.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ImplementationGateObservation {
    /// Closed gate identity.
    pub gate: ImplementationClosureGate,
    /// Passed, failed, or skipped state.
    pub result: ImplementationGateOutcome,
}

/// Complete engine-free implementation-closure candidate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ImplementationClosureObservation {
    /// Strict evidence format.
    pub format_version: ModuleAuthoringFormatVersion,
    /// Exact completeness target.
    pub target_digest: TargetDigest,
    /// Exact Feature 6 mapping identity.
    pub mapping_digest: Digest,
    /// Exact Rust implementation identity.
    pub implementation_digest: Digest,
    /// Checked generated-module asset identity.
    pub generated_assets_digest: Digest,
    /// Validated checkpoint record produced by the engine-free planner.
    pub checkpoint: CheckpointRecord,
    /// Authored list retained so omissions and duplicates remain observable.
    pub gates: Vec<ImplementationGateObservation>,
    /// Capability-local claims partitioned by their minimum local evidence domain.
    pub claims: BTreeMap<ModuleEvidenceDomain, CanonicalSet<CapabilityId>>,
}

/// Admitted local closure, kept distinct from SDK sign-off.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ImplementationClosureEvidence {
    /// Canonical identity of the complete closure observation.
    pub closure_digest: Digest,
    /// Exact completeness target.
    pub target_digest: TargetDigest,
    /// Exact Feature 6 mapping identity.
    pub mapping_digest: Digest,
    /// Exact Rust implementation identity.
    pub implementation_digest: Digest,
    /// Checked generated-module asset identity.
    pub generated_assets_digest: Digest,
    /// Locally admitted capability status changes.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Engine-dependent blockers deliberately retained after local closure.
    pub signoff_blockers: CanonicalSet<CapabilityId>,
}

/// One exact target-engine sign-off case.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleSignoffCase {
    /// Complete descriptor registration, introspection, metadata, and source maps.
    Registration,
    /// Root construction plus public and private state round trips.
    ConstructorState,
    /// Sync, async, unit, value, error, and panic-contained execution.
    ExecutionShapes,
    /// Primitive, list, optional, enum, scalar, local, interface, and explicit values.
    Types,
    /// Core, self, dependency, and current-call handles on the nested session.
    HandlesContext,
    /// Typed negative dispatch, input, application, and publication failures.
    NegativeDispatch,
    /// Concurrent call isolation and cancellation result election.
    ConcurrencyCancellation,
    /// Rust-authored packaged SDK consumer with no checkout-relative dependency.
    PackagedSelfConsumer,
    /// Applicable pinned sdk-sdk lifecycle checks in their own evidence domain.
    CommonHarness,
}

/// Immutable inputs from which one reusable target artifact identity is derived.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExactTargetArtifactInput {
    /// Exact completeness target.
    pub target_digest: TargetDigest,
    /// Full pinned Dagger revision.
    pub dagger_revision: NonEmptyText,
    /// Target operating-system and architecture spelling.
    pub platform: NonEmptyText,
    /// Combined immutable engine and CLI build input identity.
    pub engine_cli_input_digest: Digest,
    /// Mandatory engine-packaged Go runtime content identity.
    pub go_runtime_digest: Digest,
    /// Rust SDK manifest, descriptor, and source content identity.
    pub rust_content_digest: Digest,
    /// Exact Rust and Go build-toolchain identity.
    pub toolchain_digest: Digest,
}

/// One reusable exact-target artifact shared by every sign-off case.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExactTargetSignoffArtifact {
    /// Complete immutable artifact inputs.
    pub input: ExactTargetArtifactInput,
    /// Domain-separated canonical artifact identity.
    pub artifact_digest: Digest,
}

/// One case bound to the shared artifact and an exact claim subset.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSignoffCaseSpec {
    /// Stable case identity.
    pub case: ModuleSignoffCase,
    /// Shared-artifact-bound case identity.
    pub case_digest: Digest,
    /// Capabilities this case is permitted to claim.
    pub capability_ids: CanonicalSet<CapabilityId>,
}

/// Complete deferred sign-off manifest; construction performs no engine work.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSignoffManifest {
    /// Strict evidence format.
    pub format_version: ModuleAuthoringFormatVersion,
    /// Exact Feature 6 mapping identity.
    pub mapping_digest: Digest,
    /// Engine-free closure consumed rather than replayed.
    pub implementation_closure_digest: Digest,
    /// Checked generated-module assets used by every case.
    pub generated_assets_digest: Digest,
    /// Exact packaged runtime content identity.
    pub runtime_digest: Digest,
    /// One reusable target artifact.
    pub artifact: ExactTargetSignoffArtifact,
    /// Complete closed case inventory.
    pub cases: BTreeMap<ModuleSignoffCase, ModuleSignoffCaseSpec>,
}

/// Terminal state for one engine-backed sign-off case.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ModuleSignoffCaseOutcome {
    /// Case completed successfully.
    Passed { observation_digest: Digest },
    /// Case ran and failed.
    Failed { diagnostic: NonEmptyText },
    /// Case did not execute.
    Skipped { reason: NonEmptyText },
}

/// One isolated case result branched from the installed Rust baseline.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSignoffCaseObservation {
    /// Stable case identity.
    pub case: ModuleSignoffCase,
    /// Expected case digest from the manifest.
    pub case_digest: Digest,
    /// Isolated workspace identity derived from the common baseline.
    pub workspace_digest: Digest,
    /// Measured case duration.
    pub elapsed_millis: u64,
    /// Passed, failed, or skipped result.
    pub result: ModuleSignoffCaseOutcome,
}

/// Counts proving that sign-off reused its expensive resources.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSignoffExecutionShape {
    /// Target artifact build or import count.
    pub artifact_materializations: u32,
    /// Engine service start count.
    pub engine_starts: u32,
    /// Installed Rust baseline construction count.
    pub rust_baseline_installs: u32,
    /// Unrelated SDK or distribution path entries.
    pub unrelated_builds: u32,
}

/// Timings for expensive shared sign-off phases.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSignoffPhaseTimings {
    /// Build or import duration for the target artifact.
    pub artifact_build_or_import_millis: u64,
    /// Duration to start the one engine service.
    pub engine_start_millis: u64,
    /// Duration to materialize the one installed Rust baseline.
    pub rust_install_millis: u64,
}

/// Complete exact-target observation submitted for atomic sign-off admission.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSignoffObservation {
    /// Canonical manifest identity observed by the runner.
    pub manifest_digest: Digest,
    /// Exact shared artifact used by all cases.
    pub artifact_digest: Digest,
    /// Matching engine-free closure consumed by the run.
    pub implementation_closure_digest: Digest,
    /// Matching generated-module assets.
    pub generated_assets_digest: Digest,
    /// Matching packaged runtime content.
    pub runtime_digest: Digest,
    /// Resource construction/start counts.
    pub execution_shape: ModuleSignoffExecutionShape,
    /// Shared expensive-phase timings.
    pub phase_timings: ModuleSignoffPhaseTimings,
    /// Authored list retained so missing and duplicate cases remain observable.
    pub cases: Vec<ModuleSignoffCaseObservation>,
    /// Exact capability claims made by the complete run.
    pub capability_ids: CanonicalSet<CapabilityId>,
}

/// Atomic sign-off admission result, intentionally distinct from local closure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleSignoffAdmission {
    /// Digest-bound atomic verdict, absent for every rejection.
    pub verdict_digest: Option<Digest>,
    /// Permitted engine-dependent status changes.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Remaining blockers after this complete observation.
    pub blockers: CanonicalSet<CapabilityId>,
    /// Stable rejection reason, absent only for admitted sign-off.
    pub rejection: Option<NonEmptyText>,
}

/// Independently observable state of one module evidence phase.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "status", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ModuleEvidencePhase {
    /// No admitted evidence exists for this phase.
    Unexecuted,
    /// Complete evidence passed and is bound to this immutable identity.
    Passed { evidence_digest: Digest },
}

/// Feature-local report that cannot conflate implementation closure with SDK sign-off.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleAuthoringReport {
    /// Exact completeness target assessed by both phases.
    pub target_digest: TargetDigest,
    /// Reviewed module-authoring mapping identity.
    pub mapping_digest: Digest,
    /// Engine-free compiler, dispatcher, fixture, package, and security closure.
    pub implementation_closure: ModuleEvidencePhase,
    /// Deferred exact-target engine evidence.
    pub sdk_signoff: ModuleEvidencePhase,
    /// Status changes supported by the phases that actually passed.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Every capability still requiring evidence.
    pub blockers: CanonicalSet<CapabilityId>,
}

/// Constructs the one reviewed 79/32 scope plus 17-row ownership correction.
pub fn module_authoring_scope_input(target_digest: TargetDigest) -> ModuleAuthoringScopeInput {
    let mappings = FEATURE6_EXISTING_IDS
        .iter()
        .chain(FEATURE6_POLICY_IDS.iter())
        .map(|id| mapping(id, &target_digest))
        .collect();
    let ownership_corrections = FEATURE6_LIFECYCLE_CORRECTIONS
        .iter()
        .map(|id| OwnershipCorrection {
            capability_id: capability(id),
            from: FeatureId::Feature6,
            to: FeatureId::Feature5,
        })
        .collect();
    ModuleAuthoringScopeInput {
        format_version: ModuleAuthoringFormatVersion::current(),
        existing_scope_digest: Digest::new(MODULE_AUTHORING_EXISTING_SCOPE_DIGEST)
            .expect("reviewed module-authoring scope digest is valid"),
        target_digest,
        mappings,
        ownership_corrections,
    }
}

/// Validates exact set membership, target, per-row policy, and routing correction.
pub fn derive_module_authoring_scope(
    input: &ModuleAuthoringScopeInput,
    expected_target: &TargetDigest,
) -> Result<ModuleAuthoringScope, NonEmptyText> {
    let expected_digest = Digest::new(MODULE_AUTHORING_EXISTING_SCOPE_DIGEST)
        .expect("reviewed module-authoring scope digest is valid");
    if input.existing_scope_digest != expected_digest || &input.target_digest != expected_target {
        return Err(reason(
            "module authoring scope or target differs from review",
        ));
    }

    let expected_ids = expected_mapping_ids();
    let mut mappings = BTreeMap::new();
    for mapping in &input.mappings {
        if mappings
            .insert(mapping.capability_id.clone(), mapping.clone())
            .is_some()
        {
            return Err(reason("module authoring mapping contains a duplicate row"));
        }
        if mapping != &self::mapping(mapping.capability_id.as_str(), &input.target_digest) {
            return Err(reason(
                "module authoring mapping contains a stale or incomplete row",
            ));
        }
    }
    if CanonicalSet::new(mappings.keys().cloned()) != expected_ids {
        return Err(reason(
            "module authoring mapping is not the exact 79/32 set",
        ));
    }

    let expected_corrections = CanonicalSet::new(
        FEATURE6_LIFECYCLE_CORRECTIONS
            .iter()
            .map(|id| capability(id)),
    );
    let mut ownership_corrections = BTreeMap::new();
    for correction in &input.ownership_corrections {
        if correction.from != FeatureId::Feature6 || correction.to != FeatureId::Feature5 {
            return Err(reason(
                "module lifecycle correction has the wrong owner transition",
            ));
        }
        if ownership_corrections
            .insert(correction.capability_id.clone(), correction.clone())
            .is_some()
        {
            return Err(reason(
                "module lifecycle correction contains a duplicate row",
            ));
        }
    }
    if CanonicalSet::new(ownership_corrections.keys().cloned()) != expected_corrections
        || ownership_corrections
            .keys()
            .any(|id| mappings.contains_key(id))
    {
        return Err(reason(
            "module lifecycle correction is not the exact disjoint 17-row set",
        ));
    }

    let mapping_digest = canonical_digest(DigestDomain::ModuleAuthoring, &mappings)
        .map_err(|_| reason("module authoring mapping could not be hashed"))?;
    Ok(ModuleAuthoringScope {
        target_digest: input.target_digest.clone(),
        mapping_digest,
        mappings,
        ownership_corrections,
    })
}

/// Admits one capability-local observation or returns an explicit no-op rejection.
pub fn admit_module_authoring_evidence(
    scope: &ModuleAuthoringScope,
    observation: &ModuleEvidenceObservation,
) -> ModuleAuthoringEvidenceAdmission {
    let reject = |message: &'static str| ModuleAuthoringEvidenceAdmission {
        status_changes: BTreeMap::new(),
        blockers: scope.blockers(),
        rejection: Some(reason(message)),
    };
    if observation.target_digest != scope.target_digest
        || observation.mapping_digest != scope.mapping_digest
    {
        return reject("module evidence is stale or target-incompatible");
    }
    if !matches!(observation.result, ModuleEvidenceOutcome::Passed { .. }) {
        return reject("failed or skipped module evidence cannot change status");
    }
    if observation.capability_ids.is_empty() {
        return reject("module evidence proves no capability");
    }

    let mut status_changes = BTreeMap::new();
    for capability_id in observation.capability_ids.iter() {
        let Some(mapping) = scope.mappings.get(capability_id) else {
            return reject("module evidence claims a sibling or unknown capability");
        };
        if mapping.minimum_evidence_domain != observation.domain {
            return reject("module evidence domain cannot prove the claimed capability");
        }
        status_changes.insert(
            capability_id.clone(),
            match mapping.allowed_terminal_status {
                ModuleTerminalStatus::Implemented => Status::Implemented,
                ModuleTerminalStatus::IdiomaticEquivalent => Status::IdiomaticEquivalent,
                ModuleTerminalStatus::Inapplicable => Status::Inapplicable,
            },
        );
    }

    ModuleAuthoringEvidenceAdmission {
        blockers: CanonicalSet::new(
            scope
                .blockers()
                .iter()
                .filter(|id| !status_changes.contains_key(*id))
                .cloned(),
        ),
        status_changes,
        rejection: None,
    }
}

/// Returns the exact gate set required for engine-free implementation closure.
#[must_use]
pub fn required_implementation_closure_gates() -> BTreeSet<ImplementationClosureGate> {
    BTreeSet::from([
        ImplementationClosureGate::CompilerProperties,
        ImplementationClosureGate::DispatcherProperties,
        ImplementationClosureGate::CompileFixtures,
        ImplementationClosureGate::Format,
        ImplementationClosureGate::Check,
        ImplementationClosureGate::Test,
        ImplementationClosureGate::Clippy,
        ImplementationClosureGate::Rustdoc,
        ImplementationClosureGate::CargoDeny,
        ImplementationClosureGate::RepositoryRustSecurity,
        ImplementationClosureGate::GeneratedAssetDrift,
        ImplementationClosureGate::GeneratedAssetOwnership,
        ImplementationClosureGate::PublicPackagePolicy,
        ImplementationClosureGate::DirectGoAbi,
        ImplementationClosureGate::DerivedReporting,
        ImplementationClosureGate::CleanOutput,
    ])
}

/// Returns the exact local claim partition implied by the reviewed mapping.
#[must_use]
pub fn implementation_closure_claims(
    scope: &ModuleAuthoringScope,
) -> BTreeMap<ModuleEvidenceDomain, CanonicalSet<CapabilityId>> {
    let mut claims = BTreeMap::<ModuleEvidenceDomain, Vec<CapabilityId>>::new();
    for mapping in scope.mappings.values() {
        if mapping.minimum_evidence_domain != ModuleEvidenceDomain::ExactEngineSignoff {
            claims
                .entry(mapping.minimum_evidence_domain)
                .or_default()
                .push(mapping.capability_id.clone());
        }
    }
    claims
        .into_iter()
        .map(|(domain, ids)| (domain, CanonicalSet::new(ids)))
        .collect()
}

/// Admits local closure only when every exact-target, engine-free gate passed.
pub fn assemble_implementation_closure(
    scope: &ModuleAuthoringScope,
    observation: &ImplementationClosureObservation,
) -> Result<ImplementationClosureEvidence, NonEmptyText> {
    if observation.target_digest != scope.target_digest
        || observation.mapping_digest != scope.mapping_digest
        || observation.implementation_digest.as_str()
            != observation.checkpoint.implementation_digest.as_str()
    {
        return Err(reason(
            "implementation closure is stale or target-incompatible",
        ));
    }
    if let CheckpointGenerationDecision::ScopedRefresh {
        changed_domains, ..
    } = &observation.checkpoint.generation
        && changed_domains.is_empty()
    {
        return Err(reason(
            "implementation closure contains an unowned regeneration",
        ));
    }
    if observation
        .checkpoint
        .actions
        .iter()
        .any(|item| item.elapsed_millis == 0 || item.outcome != CheckpointActionOutcome::Passed)
    {
        return Err(reason(
            "implementation closure checkpoint is incomplete or did not pass",
        ));
    }

    let mut gates = BTreeMap::new();
    for gate in &observation.gates {
        if gates.insert(gate.gate, gate.result.clone()).is_some() {
            return Err(reason("implementation closure contains a duplicate gate"));
        }
    }
    if CanonicalSet::new(gates.keys().copied())
        != CanonicalSet::new(required_implementation_closure_gates())
        || gates
            .values()
            .any(|outcome| !matches!(outcome, ImplementationGateOutcome::Passed { .. }))
    {
        return Err(reason(
            "implementation closure is missing, skipped, or failed a required gate",
        ));
    }

    let expected_claims = implementation_closure_claims(scope);
    if observation.claims != expected_claims
        || observation
            .claims
            .contains_key(&ModuleEvidenceDomain::ExactEngineSignoff)
    {
        return Err(reason(
            "implementation closure claims are incomplete, overbroad, or engine-backed",
        ));
    }

    let closure_digest = canonical_digest(DigestDomain::ModuleAuthoring, observation)
        .map_err(|_| reason("implementation closure could not be hashed"))?;
    let mut status_changes = BTreeMap::new();
    for (domain, capability_ids) in &observation.claims {
        let admission = admit_module_authoring_evidence(
            scope,
            &ModuleEvidenceObservation {
                format_version: ModuleAuthoringFormatVersion::current(),
                evidence_id: EvidenceId::new(format!(
                    "verification/module-authoring/implementation-closure/{}",
                    evidence_domain_slug(*domain)
                ))
                .expect("closed evidence-domain spelling is non-empty"),
                target_digest: observation.target_digest.clone(),
                mapping_digest: observation.mapping_digest.clone(),
                domain: *domain,
                capability_ids: capability_ids.clone(),
                result: ModuleEvidenceOutcome::Passed {
                    observation_digest: closure_digest.clone(),
                },
            },
        );
        if admission.rejection.is_some() {
            return Err(reason(
                "implementation closure failed capability-local evidence admission",
            ));
        }
        status_changes.extend(admission.status_changes);
    }
    let signoff_blockers = CanonicalSet::new(
        scope
            .blockers()
            .iter()
            .filter(|id| !status_changes.contains_key(*id))
            .cloned(),
    );
    Ok(ImplementationClosureEvidence {
        closure_digest,
        target_digest: observation.target_digest.clone(),
        mapping_digest: observation.mapping_digest.clone(),
        implementation_digest: observation.implementation_digest.clone(),
        generated_assets_digest: observation.generated_assets_digest.clone(),
        status_changes,
        signoff_blockers,
    })
}

/// Builds the one immutable target artifact identity used by all sign-off cases.
pub fn build_exact_target_signoff_artifact(
    input: ExactTargetArtifactInput,
) -> Result<ExactTargetSignoffArtifact, NonEmptyText> {
    if input.dagger_revision.as_str() != "25300124ca110612edc09c43f89cb5fad6028170" {
        return Err(reason("sign-off artifact uses the wrong Dagger revision"));
    }
    let artifact_digest = canonical_digest(DigestDomain::ModuleAuthoring, &input)
        .map_err(|_| reason("sign-off artifact could not be hashed"))?;
    Ok(ExactTargetSignoffArtifact {
        input,
        artifact_digest,
    })
}

/// Constructs the complete deferred case inventory without starting an engine.
pub fn build_module_signoff_manifest(
    scope: &ModuleAuthoringScope,
    closure: &ImplementationClosureEvidence,
    artifact: ExactTargetSignoffArtifact,
    runtime_digest: Digest,
) -> Result<ModuleSignoffManifest, NonEmptyText> {
    if closure.target_digest != scope.target_digest
        || closure.mapping_digest != scope.mapping_digest
        || closure.target_digest != artifact.input.target_digest
        || build_exact_target_signoff_artifact(artifact.input.clone())? != artifact
    {
        return Err(reason(
            "sign-off manifest combines stale or cross-target identities",
        ));
    }
    let engine_claims = exact_signoff_claims(scope);
    if closure.signoff_blockers != engine_claims {
        return Err(reason(
            "sign-off manifest does not consume complete local closure",
        ));
    }
    let mut cases = BTreeMap::new();
    for case in required_module_signoff_cases() {
        let capability_ids = if case == ModuleSignoffCase::Registration {
            engine_claims.clone()
        } else {
            CanonicalSet::default()
        };
        let case_digest = canonical_digest(
            DigestDomain::ModuleAuthoring,
            &(case, &artifact.artifact_digest, &capability_ids),
        )
        .map_err(|_| reason("sign-off case could not be hashed"))?;
        cases.insert(
            case,
            ModuleSignoffCaseSpec {
                case,
                case_digest,
                capability_ids,
            },
        );
    }
    Ok(ModuleSignoffManifest {
        format_version: ModuleAuthoringFormatVersion::current(),
        mapping_digest: scope.mapping_digest.clone(),
        implementation_closure_digest: closure.closure_digest.clone(),
        generated_assets_digest: closure.generated_assets_digest.clone(),
        runtime_digest,
        artifact,
        cases,
    })
}

/// Returns the complete closed engine-backed case inventory.
#[must_use]
pub fn required_module_signoff_cases() -> BTreeSet<ModuleSignoffCase> {
    BTreeSet::from([
        ModuleSignoffCase::Registration,
        ModuleSignoffCase::ConstructorState,
        ModuleSignoffCase::ExecutionShapes,
        ModuleSignoffCase::Types,
        ModuleSignoffCase::HandlesContext,
        ModuleSignoffCase::NegativeDispatch,
        ModuleSignoffCase::ConcurrencyCancellation,
        ModuleSignoffCase::PackagedSelfConsumer,
        ModuleSignoffCase::CommonHarness,
    ])
}

/// Atomically admits a complete exact-target sign-off observation.
pub fn admit_module_signoff(
    scope: &ModuleAuthoringScope,
    manifest: &ModuleSignoffManifest,
    observation: &ModuleSignoffObservation,
) -> ModuleSignoffAdmission {
    let reject = |message: &'static str| ModuleSignoffAdmission {
        verdict_digest: None,
        status_changes: BTreeMap::new(),
        blockers: scope.blockers(),
        rejection: Some(reason(message)),
    };
    let Ok(expected_artifact) =
        build_exact_target_signoff_artifact(manifest.artifact.input.clone())
    else {
        return reject("sign-off artifact is not canonical or exact-target");
    };
    let Ok(manifest_digest) = canonical_digest(DigestDomain::ModuleAuthoring, manifest) else {
        return reject("sign-off manifest could not be hashed");
    };
    if expected_artifact != manifest.artifact
        || observation.manifest_digest != manifest_digest
        || observation.artifact_digest != manifest.artifact.artifact_digest
        || observation.implementation_closure_digest != manifest.implementation_closure_digest
        || observation.generated_assets_digest != manifest.generated_assets_digest
        || observation.runtime_digest != manifest.runtime_digest
    {
        return reject("sign-off observation is stale or cross-target");
    }
    if observation.execution_shape.artifact_materializations != 1
        || observation.execution_shape.engine_starts != 1
        || observation.execution_shape.rust_baseline_installs != 1
        || observation.execution_shape.unrelated_builds != 0
    {
        return reject("sign-off did not reuse one artifact, engine, and Rust baseline");
    }
    if observation.phase_timings.artifact_build_or_import_millis == 0
        || observation.phase_timings.engine_start_millis == 0
        || observation.phase_timings.rust_install_millis == 0
    {
        return reject("sign-off omitted an expensive phase timing");
    }

    let mut cases = BTreeMap::new();
    for case in &observation.cases {
        let Some(spec) = manifest.cases.get(&case.case) else {
            return reject("sign-off observation contains an unknown case");
        };
        if case.case_digest != spec.case_digest
            || case.elapsed_millis == 0
            || !matches!(case.result, ModuleSignoffCaseOutcome::Passed { .. })
            || cases.insert(case.case, case).is_some()
        {
            return reject("sign-off case is stale, duplicated, skipped, or failed");
        }
    }
    if CanonicalSet::new(cases.keys().copied())
        != CanonicalSet::new(required_module_signoff_cases())
    {
        return reject("sign-off observation omits a required case");
    }

    let expected_claims = exact_signoff_claims(scope);
    let manifest_claims = CanonicalSet::new(
        manifest
            .cases
            .values()
            .flat_map(|case| case.capability_ids.iter().cloned()),
    );
    if manifest_claims != expected_claims || observation.capability_ids != expected_claims {
        return reject("sign-off capability claims are incomplete or overbroad");
    }
    let verdict_digest = match canonical_digest(DigestDomain::ModuleAuthoring, observation) {
        Ok(digest) => digest,
        Err(_) => return reject("sign-off verdict could not be hashed"),
    };
    let admission = admit_module_authoring_evidence(
        scope,
        &ModuleEvidenceObservation {
            format_version: ModuleAuthoringFormatVersion::current(),
            evidence_id: EvidenceId::new("verification/module-authoring/sdk-signoff")
                .expect("static sign-off evidence identity is valid"),
            target_digest: scope.target_digest.clone(),
            mapping_digest: scope.mapping_digest.clone(),
            domain: ModuleEvidenceDomain::ExactEngineSignoff,
            capability_ids: expected_claims,
            result: ModuleEvidenceOutcome::Passed {
                observation_digest: verdict_digest.clone(),
            },
        },
    );
    if admission.rejection.is_some() {
        return reject("sign-off claims failed capability-local evidence admission");
    }
    ModuleSignoffAdmission {
        verdict_digest: Some(verdict_digest),
        status_changes: admission.status_changes,
        // The manifest can exist only after exact local closure, so the one admitted
        // engine-domain partition is the complete residual blocker set.
        blockers: CanonicalSet::default(),
        rejection: None,
    }
}

/// Derives the honest module report from independently admitted closure and sign-off.
pub fn derive_module_authoring_report(
    scope: &ModuleAuthoringScope,
    closure: Option<&ImplementationClosureEvidence>,
    signoff: Option<&ModuleSignoffAdmission>,
) -> Result<ModuleAuthoringReport, NonEmptyText> {
    if signoff.is_some() && closure.is_none() {
        return Err(reason("SDK sign-off cannot precede implementation closure"));
    }

    let mut status_changes = BTreeMap::new();
    let implementation_closure = if let Some(closure) = closure {
        if closure.target_digest != scope.target_digest
            || closure.mapping_digest != scope.mapping_digest
            || closure.status_changes
                != expected_status_changes(scope, |mapping| {
                    mapping.minimum_evidence_domain != ModuleEvidenceDomain::ExactEngineSignoff
                })
            || closure.signoff_blockers != exact_signoff_claims(scope)
        {
            return Err(reason(
                "implementation closure report input is stale or incomplete",
            ));
        }
        status_changes.extend(closure.status_changes.clone());
        ModuleEvidencePhase::Passed {
            evidence_digest: closure.closure_digest.clone(),
        }
    } else {
        ModuleEvidencePhase::Unexecuted
    };

    let sdk_signoff = if let Some(signoff) = signoff {
        let Some(verdict_digest) = &signoff.verdict_digest else {
            return Err(reason("rejected SDK sign-off cannot enter the report"));
        };
        if signoff.rejection.is_some()
            || signoff.status_changes
                != expected_status_changes(scope, |mapping| {
                    mapping.minimum_evidence_domain == ModuleEvidenceDomain::ExactEngineSignoff
                })
            || !signoff.blockers.is_empty()
        {
            return Err(reason(
                "SDK sign-off report input is rejected or incomplete",
            ));
        }
        status_changes.extend(signoff.status_changes.clone());
        ModuleEvidencePhase::Passed {
            evidence_digest: verdict_digest.clone(),
        }
    } else {
        ModuleEvidencePhase::Unexecuted
    };

    let blockers = CanonicalSet::new(
        scope
            .blockers()
            .iter()
            .filter(|capability_id| !status_changes.contains_key(*capability_id))
            .cloned(),
    );
    Ok(ModuleAuthoringReport {
        target_digest: scope.target_digest.clone(),
        mapping_digest: scope.mapping_digest.clone(),
        implementation_closure,
        sdk_signoff,
        status_changes,
        blockers,
    })
}

fn expected_status_changes(
    scope: &ModuleAuthoringScope,
    include: impl Fn(&ModuleAuthoringMapping) -> bool,
) -> BTreeMap<CapabilityId, Status> {
    scope
        .mappings
        .values()
        .filter(|mapping| include(mapping))
        .map(|mapping| {
            let status = match mapping.allowed_terminal_status {
                ModuleTerminalStatus::Implemented => Status::Implemented,
                ModuleTerminalStatus::IdiomaticEquivalent => Status::IdiomaticEquivalent,
                ModuleTerminalStatus::Inapplicable => Status::Inapplicable,
            };
            (mapping.capability_id.clone(), status)
        })
        .collect()
}

fn exact_signoff_claims(scope: &ModuleAuthoringScope) -> CanonicalSet<CapabilityId> {
    CanonicalSet::new(
        scope
            .mappings
            .values()
            .filter(|mapping| {
                mapping.minimum_evidence_domain == ModuleEvidenceDomain::ExactEngineSignoff
            })
            .map(|mapping| mapping.capability_id.clone()),
    )
}

const fn evidence_domain_slug(domain: ModuleEvidenceDomain) -> &'static str {
    match domain {
        ModuleEvidenceDomain::CompileFixture => "compile-fixture",
        ModuleEvidenceDomain::CompilerProperty => "compiler-property",
        ModuleEvidenceDomain::DispatchProperty => "dispatch-property",
        ModuleEvidenceDomain::AssetProperty => "asset-property",
        ModuleEvidenceDomain::SecurityHygiene => "security-hygiene",
        ModuleEvidenceDomain::ExactEngineSignoff => "exact-engine-signoff",
        ModuleEvidenceDomain::SiblingStandaloneClient => "sibling-standalone-client",
        ModuleEvidenceDomain::LifecycleIntegration => "lifecycle-integration",
        ModuleEvidenceDomain::CrossPlatform => "cross-platform",
    }
}

fn mapping(id: &str, target_digest: &TargetDigest) -> ModuleAuthoringMapping {
    let authority = authority_for(id);
    let implementation_subject = subject_for(id, authority);
    let minimum_evidence_domain = evidence_for(id, implementation_subject);
    ModuleAuthoringMapping {
        capability_id: capability(id),
        authority,
        requirement: NonEmptyText::new(requirement_for(id, authority))
            .expect("reviewed requirement coordinate is valid"),
        implementation_subject,
        rationale: NonEmptyText::new(rationale_for(authority))
            .expect("reviewed mapping rationale is valid"),
        allowed_terminal_status: terminal_for(id, authority),
        minimum_evidence_domain,
        target_digest: target_digest.clone(),
        blocker: true,
    }
}

fn authority_for(id: &str) -> ModuleAuthority {
    if id.starts_with("behavior/go-codegen/") {
        ModuleAuthority::GoCodegen
    } else if id.starts_with("behavior/go-client/") {
        ModuleAuthority::GoClient
    } else {
        ModuleAuthority::RustPolicy
    }
}

fn subject_for(id: &str, authority: ModuleAuthority) -> ModuleImplementationSubject {
    if authority == ModuleAuthority::GoClient
        || id.contains("active-session-context")
        || id.contains("self-dependency-context")
    {
        ModuleImplementationSubject::ModuleContext
    } else if id.contains("generated-asset") || id.contains("regeneration") {
        ModuleImplementationSubject::GeneratedAssets
    } else if id.contains("dispatch")
        || id.contains("argument")
        || id.contains("result")
        || id.contains("panic")
        || id.contains("cancellation")
        || id.contains("call-isolation")
        || id.contains("parent-state")
        || id.contains("object-id")
    {
        ModuleImplementationSubject::DispatchRuntime
    } else if id.contains("explicit-export") || id.contains("authoring-single-source") {
        ModuleImplementationSubject::AuthoringBridge
    } else if id.contains("engine-free")
        || id.contains("signoff-boundary")
        || id.contains("source-coordinate-diagnostics")
    {
        ModuleImplementationSubject::EvidenceBoundary
    } else if authority == ModuleAuthority::GoCodegen
        || id.contains("descriptor")
        || id.contains("typedef")
        || id.contains("type-mapping")
        || id.contains("optional-default")
        || id.contains("function-")
        || id.contains("interface")
        || id.contains("enum")
        || id.contains("scalar")
        || id.contains("object-state")
        || id.contains("private-state")
        || id.contains("wire-name")
        || id.contains("root-constructor")
    {
        ModuleImplementationSubject::TypeProjection
    } else {
        ModuleImplementationSubject::SourceCompiler
    }
}

fn evidence_for(id: &str, subject: ModuleImplementationSubject) -> ModuleEvidenceDomain {
    if id.contains("exact-engine-signoff-boundary") {
        ModuleEvidenceDomain::ExactEngineSignoff
    } else {
        match subject {
            ModuleImplementationSubject::AuthoringBridge => ModuleEvidenceDomain::CompileFixture,
            ModuleImplementationSubject::SourceCompiler
            | ModuleImplementationSubject::TypeProjection => ModuleEvidenceDomain::CompilerProperty,
            ModuleImplementationSubject::DispatchRuntime
            | ModuleImplementationSubject::ModuleContext => ModuleEvidenceDomain::DispatchProperty,
            ModuleImplementationSubject::GeneratedAssets => ModuleEvidenceDomain::AssetProperty,
            ModuleImplementationSubject::EvidenceBoundary => ModuleEvidenceDomain::SecurityHygiene,
        }
    }
}

fn terminal_for(id: &str, authority: ModuleAuthority) -> ModuleTerminalStatus {
    if authority == ModuleAuthority::GoClient
        && (id.contains("%254%41%2553%254%46%254%45") || id.contains("%254%43%254%43%254%44"))
    {
        // These generated Go globals are type aliases with no sound Rust symbol; their
        // observable operations remain available through the typed module context.
        ModuleTerminalStatus::IdiomaticEquivalent
    } else {
        ModuleTerminalStatus::Implemented
    }
}

fn requirement_for(id: &str, authority: ModuleAuthority) -> &'static str {
    if authority == ModuleAuthority::GoClient {
        "12.9"
    } else if id.contains("explicit-export") {
        "2.1"
    } else if id.contains("authoring-single-source") {
        "2.6"
    } else if id.contains("source-discovery") {
        "3.3"
    } else if id.contains("source-coordinate") {
        "14.2"
    } else if id.contains("wire-name") {
        "7.14"
    } else if id.contains("root-constructor") {
        "4.10"
    } else if id.contains("object-state") {
        "4.2"
    } else if id.contains("private-state") {
        "4.4"
    } else if id.contains("interface") {
        "5.1"
    } else if id.contains("enum") {
        "5.6"
    } else if id.contains("custom-scalar") {
        "5.11"
    } else if id.contains("type-mapping") {
        "6.1"
    } else if id.contains("optional-default") {
        "6.7"
    } else if id.contains("function-shape") {
        "7.1"
    } else if id.contains("function-metadata") {
        "7.6"
    } else if id.contains("canonical-descriptor") {
        "8.1"
    } else if id.contains("typedef-introspection") {
        "8.7"
    } else if id.contains("dispatch-totality") {
        "9.4"
    } else if id.contains("parent-state") {
        "10.1"
    } else if id.contains("named-argument") {
        "10.4"
    } else if id.contains("object-id") {
        "10.12"
    } else if id.contains("result-single") {
        "11.10"
    } else if id.contains("application-error") {
        "11.6"
    } else if id.contains("panic") {
        "11.8"
    } else if id.contains("active-session") {
        "12.1"
    } else if id.contains("self-dependency") {
        "12.5"
    } else if id.contains("call-isolation") {
        "13.1"
    } else if id.contains("cancellation") {
        "13.7"
    } else if id.contains("generated-asset") {
        "15.1"
    } else if id.contains("regeneration") {
        "15.7"
    } else if id.contains("engine-free") {
        "16.1"
    } else if id.contains("signoff-boundary") {
        "17.9"
    } else {
        "8.2"
    }
}

const fn rationale_for(authority: ModuleAuthority) -> &'static str {
    match authority {
        ModuleAuthority::GoCodegen => {
            "Preserve the definitive observable generator behaviour through an idiomatic typed Rust compiler"
        }
        ModuleAuthority::GoClient => {
            "Preserve the definitive helper capability through call-scoped Rust context without process-global state"
        }
        ModuleAuthority::RustPolicy => {
            "Make the approved Rust-native authoring and dispatch obligation independently provable"
        }
    }
}

fn expected_mapping_ids() -> CanonicalSet<CapabilityId> {
    CanonicalSet::new(
        FEATURE6_EXISTING_IDS
            .iter()
            .chain(FEATURE6_POLICY_IDS.iter())
            .map(|id| capability(id)),
    )
}

fn capability(id: &str) -> CapabilityId {
    CapabilityId::new(id).expect("reviewed module capability identity is valid")
}

fn reason(message: &'static str) -> NonEmptyText {
    NonEmptyText::new(message).expect("static module scope diagnostic is valid")
}

const FEATURE6_POLICY_IDS: &[&str] = &[
    "policy/rust-policy/module-explicit-export",
    "policy/rust-policy/module-authoring-single-source",
    "policy/rust-policy/module-source-discovery-closure",
    "policy/rust-policy/module-source-coordinate-diagnostics",
    "policy/rust-policy/module-wire-name-collision",
    "policy/rust-policy/module-root-constructor",
    "policy/rust-policy/module-object-state",
    "policy/rust-policy/module-private-state",
    "policy/rust-policy/module-interface-contract",
    "policy/rust-policy/module-enum-contract",
    "policy/rust-policy/module-custom-scalar-contract",
    "policy/rust-policy/module-type-mapping-closure",
    "policy/rust-policy/module-optional-default-semantics",
    "policy/rust-policy/module-function-shape-closure",
    "policy/rust-policy/module-function-metadata",
    "policy/rust-policy/module-canonical-descriptor",
    "policy/rust-policy/module-typedef-introspection-equivalence",
    "policy/rust-policy/module-dispatch-totality",
    "policy/rust-policy/module-parent-state-decoding",
    "policy/rust-policy/module-named-argument-decoding",
    "policy/rust-policy/module-object-id-reentry",
    "policy/rust-policy/module-result-single-assignment",
    "policy/rust-policy/module-application-error-reporting",
    "policy/rust-policy/module-panic-containment",
    "policy/rust-policy/module-active-session-context",
    "policy/rust-policy/module-self-dependency-context",
    "policy/rust-policy/module-call-isolation",
    "policy/rust-policy/module-cancellation",
    "policy/rust-policy/module-generated-asset-ownership",
    "policy/rust-policy/module-change-triggered-regeneration",
    "policy/rust-policy/module-engine-free-local-checkpoint",
    "policy/rust-policy/module-exact-engine-signoff-boundary",
];

const FEATURE6_LIFECYCLE_CORRECTIONS: &[&str] = &[
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fdeps-list-succeeds",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fengine-required-reports-version",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fgenerate-exposes-generator",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fgenerate-respects-cwd",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fgenerate-succeeds",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-does-not-remove-existing-files",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-does-not-write-config",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-honors-custom-path",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-seeds-files",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-records-authoring-sdk",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-registers-module",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-scaffolds-module",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-writes-module-config",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finstall-marks-as-sdk",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finstall-registers-sdk",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fscaffolded-module-loads",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fsdk-reports-module-options",
];

const FEATURE6_EXISTING_IDS: &[&str] = &[
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%41%2553%254%46%254%45",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%43%254%43%254%44",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%44odule",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%44odule%2553ource",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%45ode",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2541ddress",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543ache%2556olume",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543hangeset",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543lose",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543loud",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543ontainer",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%254%44odule",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%254%45ode",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%2546unction%2543all",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%2554ype%2544efs",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%2557orkspace",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2544efault%2550latform",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2544irectory",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545ngine",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545ngine%2556olume",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545nv%2546ile",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545rror",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2546ile",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2546unction",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2547enerated%2543ode",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2547it",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2548%2554%2554%2550",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2548ost",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2549%2544",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553chema",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553ecret",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553et%2553ecret",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553ource%254%44ap",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553shfs%2556olume",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2554ype%2544ef",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2556ersion",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-dynamic-subtest%2Ftemplates%2F%2554est%2550arse%2550ragma%2543omment%2F%253%43dynamic%253%41de17bc76acebbaa7%253%410%253%45",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-function%2Ftemplates%2F%254%45ew%254%44odule%2549ntrospection%2545mitter",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-function%2Ftemplates%2F%2547o%2554emplate%2546uncs",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-subtest%2Ftemplates%2F%2554est%2556isit%2554ypes%2544eterministic%2541cross%2546iles%2Fgitrepo%2520method%2520order",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-subtest%2Ftemplates%2F%2554est%2556isit%2554ypes%2544eterministic%2541cross%2546iles%2Fstruct%2520order",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%43egacy%254%44odule%2549nterface%2549%2544%2553urface",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%44odule%2549ntrospection%254%41%2553%254%46%254%45_%2550arseable",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%44odule%2549ntrospection%254%41%2553%254%46%254%45_%2552ound%2554rips%2554hrough%254%44erge",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%45amespace%2554ype%254%45ame",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%46bject%2555nmarshal%2549mported%2549nterface%2546ield",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2542asic",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2553kip%2543ontext%2541rg",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2553kips%2552eserved%2549%2544",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2556oid%2552eturn",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2557ith%2546ield",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%254%46bjects%2550ass%2542y%2549%2544",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%254%46ptional%254%45on%254%45ull%2553tripping",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%2545num%2544efault",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%2550ointer%254%46bject%2545num%2552equired",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%2556ariadic%2541nd%2544efault%2553tay%2552equired",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2545num_%2542asic",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2545num_%2543onventional%254%44ember%254%45ames",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2549nterface_%2542asic",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%254%45ame_%254%43ocal%2556s%2546oreign",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%254%46bject%2552ef",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%254%46bject%2552ef%2550ointer",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2545num%2552ef",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2549face%2552ef",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2542ool",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2546loat",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2549nt",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2550ointer",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2553lice",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2553lice%254%46f%254%45on%254%45ull%254%46bjects",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2553lice%254%46f%254%46bjects",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2550arse%2547o%2549face%2541ccepts%2549mported%2544agger%254%46bject",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2550arse%2550ragma%2543omment",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2556isit%2554ypes%2544eterministic%2541cross%2546iles",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test-table%2Ftemplates%2F%2554est%2550arse%2550ragma%2543omment%2F%253%43table%253%41de17bc76acebbaa7%253%410%253%45",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Ftemplates%2F%254%44odule%2549ntrospection%2545mitter",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Ftemplates%2F%254%45amed%2550arsed%2554ype",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Ftemplates%2F%2550arsed%2554ype",
];

#[cfg(test)]
mod tests {
    use super::{
        FEATURE6_EXISTING_IDS, FEATURE6_LIFECYCLE_CORRECTIONS, FEATURE6_POLICY_IDS,
        derive_module_authoring_scope, module_authoring_scope_input,
    };
    use crate::model::{Digest, TargetDigest};

    #[test]
    fn reviewed_partition_has_exact_counts_and_is_disjoint() {
        assert_eq!(FEATURE6_EXISTING_IDS.len(), 79);
        assert_eq!(FEATURE6_POLICY_IDS.len(), 32);
        assert_eq!(FEATURE6_LIFECYCLE_CORRECTIONS.len(), 17);
        let existing = FEATURE6_EXISTING_IDS
            .iter()
            .copied()
            .collect::<std::collections::BTreeSet<_>>();
        let policies = FEATURE6_POLICY_IDS
            .iter()
            .copied()
            .collect::<std::collections::BTreeSet<_>>();
        let corrections = FEATURE6_LIFECYCLE_CORRECTIONS
            .iter()
            .copied()
            .collect::<std::collections::BTreeSet<_>>();
        assert!(existing.is_disjoint(&policies));
        assert!(existing.is_disjoint(&corrections));
        assert!(policies.is_disjoint(&corrections));
    }

    #[test]
    fn reviewed_scope_derives_without_mutating_status() {
        let target = TargetDigest::new(Digest::sha256(b"module-authoring-target"));
        let input = module_authoring_scope_input(target.clone());
        let scope = derive_module_authoring_scope(&input, &target).expect("reviewed scope derives");
        assert_eq!(scope.mappings().len(), 111);
        assert_eq!(scope.ownership_corrections().len(), 17);
        assert_eq!(scope.blockers().len(), 111);
    }
}
