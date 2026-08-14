//! Closed planning and observation for Rust-first, engine-free checkpoints.
//!
//! Checkpoint commands are represented as typed actions rather than shell text.  The
//! validated form cannot name Dagger, an engine, a network graph, or another SDK, and
//! an execution record is admitted only when it accounts for every planned action
//! exactly once.  A requested engine observation is retained only as an approved,
//! deferred sign-off exception; it never becomes a local checkpoint action.

use std::collections::{BTreeMap, BTreeSet};

use dagger_codegen::module::{
    ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet, RegenerationClass,
};
use serde::{Deserialize, Serialize};

use crate::Sha256Digest;
use crate::canonical::{DigestDomain, canonical_digest};

/// Rust workspace packages admitted by the local checkpoint planner.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CheckpointPackage {
    /// Public procedural-macro companion.
    DaggerSdkMacros,
    /// Private schema and module compiler.
    DaggerCodegen,
    /// Public Rust SDK.
    DaggerSdk,
    /// Private engine adapter and asset owner.
    DaggerSdkEngine,
    /// Private completeness and evidence contract.
    DaggerSdkCompleteness,
    /// Private generation orchestrator.
    DaggerBootstrap,
}

/// Public packages whose exact publication contents are checked locally.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PublicCheckpointPackage {
    /// Public SDK package.
    DaggerSdk,
    /// Public procedural-macro companion.
    DaggerSdkMacros,
}

/// Direct Go ABI package owned by the Rust SDK.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum RustGoAbiPackage {
    /// The complete confined Rust SDK runtime adapter.
    Runtime,
    /// The runtime metadata package used by the repository security gate.
    RuntimeMetadata,
}

/// Stable identity for a selected Rust test target.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize)]
pub struct CheckpointTestTarget(Box<str>);

impl CheckpointTestTarget {
    /// Constructs a Cargo test target identity without admitting command syntax.
    pub fn new(value: impl Into<String>) -> Result<Self, String> {
        let value = value.into();
        if value.is_empty()
            || value.len() > 128
            || !value
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-'))
        {
            return Err("checkpoint test target is not a stable Cargo target name".to_owned());
        }
        Ok(Self(value.into_boxed_str()))
    }

    /// Borrows the stable target spelling.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl<'de> Deserialize<'de> for CheckpointTestTarget {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        Self::new(value).map_err(serde::de::Error::custom)
    }
}

/// One numbered module-authoring correctness property.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize)]
pub struct ModuleProperty(u8);

impl ModuleProperty {
    /// Constructs one of the complete 32 property identities.
    pub fn new(value: u8) -> Result<Self, String> {
        if (1..=32).contains(&value) {
            Ok(Self(value))
        } else {
            Err("module property identity must be between 1 and 32".to_owned())
        }
    }

    /// Returns the stable property number.
    #[must_use]
    pub const fn get(self) -> u8 {
        self.0
    }
}

impl<'de> Deserialize<'de> for ModuleProperty {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        Self::new(u8::deserialize(deserializer)?).map_err(serde::de::Error::custom)
    }
}

/// Closed executable action set for a local checkpoint.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(tag = "action", rename_all = "kebab-case", deny_unknown_fields)]
pub enum CheckpointAction {
    /// Verify workspace formatting without changing files.
    Format {
        /// Exact package closure whose workspace is formatted.
        packages: BTreeSet<CheckpointPackage>,
    },
    /// Type-check one Rust package using the committed lockfile.
    Check {
        /// Exact package selected by Cargo.
        package: CheckpointPackage,
        /// Whether the package's complete feature surface is checked.
        all_features: bool,
    },
    /// Test one Rust package using bounded named targets and properties.
    Test {
        /// Exact package selected by Cargo.
        package: CheckpointPackage,
        /// Empty means the package's ordinary test inventory; nonempty is a bounded slice.
        targets: BTreeSet<CheckpointTestTarget>,
        /// Properties explicitly accounted for by this test action.
        properties: BTreeSet<ModuleProperty>,
    },
    /// Run warning-denied Clippy over the selected Rust packages.
    Clippy {
        /// Exact package set, never an unrelated repository workspace.
        packages: BTreeSet<CheckpointPackage>,
    },
    /// Build warning-denied rustdoc without dependencies.
    Rustdoc {
        /// Exact package set, never an unrelated repository workspace.
        packages: BTreeSet<CheckpointPackage>,
    },
    /// Evaluate the checked Cargo Deny policy.
    CargoDeny,
    /// Execute the repository's bounded Rust security command set.
    RepositoryRustSecurity,
    /// Compare generated module assets with their ownership manifest.
    GeneratedAssetDrift,
    /// Verify the contents of one public package.
    PackageContents {
        /// Public package being assembled and inspected.
        package: PublicCheckpointPackage,
    },
    /// Run the Rust SDK's direct, engine-free Go ABI tests.
    DirectGoAbi {
        /// Exact Rust-owned Go package selector.
        package: RustGoAbiPackage,
    },
    /// Verify that derived output and the reviewed source tree are byte-clean.
    CleanOutput,
}

/// Boundary that can be proposed but can never enter a validated local plan.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ForbiddenCheckpointBoundary {
    /// A Dagger engine build or process.
    Engine,
    /// A Dagger CLI or module invocation.
    Dagger,
    /// A Dagger module execution even when no CLI command is retained.
    Module,
    /// A network-backed build graph.
    NetworkGraph,
    /// An unrelated language SDK builder, test, or generator.
    OtherSdk,
    /// An unscoped repository workspace generator.
    UnscopedGeneration,
    /// A distribution-wide build path.
    Distribution,
    /// Exact-target artifact construction or import.
    TargetArtifact,
    /// Exact-target vulnerability scanning.
    TargetArtifactScan,
    /// Caller-supplied shell or process text.
    ArbitraryShell,
}

/// Authored request item before the engine-free boundary is validated.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "proposal", rename_all = "kebab-case", deny_unknown_fields)]
pub enum CheckpointProposal {
    /// A typed Rust-owned action.
    Action { action: CheckpointAction },
    /// A boundary which must be rejected rather than rendered as a command.
    Forbidden {
        /// Exact forbidden domain that was requested.
        boundary: ForbiddenCheckpointBoundary,
    },
}

/// Checked-asset decision recorded once for a checkpoint.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "decision", rename_all = "kebab-case", deny_unknown_fields)]
pub enum CheckpointGenerationDecision {
    /// All owning inputs are unchanged, so checked assets are consumed directly.
    ReuseChecked {
        /// Identity of the checked generated-module asset manifest.
        manifest_digest: Sha256Digest,
    },
    /// Owning inputs changed and authorize one scoped refresh before checked use.
    ScopedRefresh {
        /// Nonempty changed input domains which authorize the refresh.
        changed_domains: BTreeSet<RegenerationClass>,
        /// Identity of the resulting checked asset manifest.
        manifest_digest: Sha256Digest,
    },
}

/// Reviewed exception retained for the later sign-off inventory only.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DeferredSignoffException {
    /// Exact contract impossible to observe through the direct model.
    pub contract_gap: String,
    /// Reviewed proof that the production direct model cannot represent the contract.
    pub model_insufficiency: String,
    /// Smallest proposed sign-off case.
    pub proposed_case: String,
    /// Explicit human approval; absence keeps the whole plan invalid.
    pub approved: bool,
}

/// Unvalidated local checkpoint request.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckpointRequest {
    /// Exact implementation identity being checked.
    pub implementation_digest: Sha256Digest,
    /// Proposed actions and forbidden-boundary probes.
    pub proposals: Vec<CheckpointProposal>,
    /// One generated-asset reuse or refresh decision.
    pub generation: CheckpointGenerationDecision,
    /// Optional approved case retained only for deferred sign-off.
    pub deferred_signoff_exception: Option<DeferredSignoffException>,
}

/// Validated local plan whose action set is engine-free by construction.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckpointPlan {
    /// Exact implementation identity being checked.
    pub implementation_digest: Sha256Digest,
    /// Duplicate-free typed action set.
    pub actions: BTreeSet<CheckpointAction>,
    /// One generated-asset reuse or refresh decision.
    pub generation: CheckpointGenerationDecision,
    /// Approved case retained only for deferred sign-off.
    pub deferred_signoff_exception: Option<DeferredSignoffException>,
}

/// Terminal result of one planned action.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CheckpointActionOutcome {
    /// The action completed successfully.
    Passed,
    /// The action ran and failed.
    Failed,
    /// The action did not run.
    Skipped,
}

/// One elapsed observation for one planned action.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckpointActionObservation {
    /// Planned typed action.
    pub action: CheckpointAction,
    /// Terminal result.
    pub outcome: CheckpointActionOutcome,
    /// Measured wall time in milliseconds.
    pub elapsed_millis: u64,
}

/// Complete execution observation, including process-boundary auditing.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckpointObservation {
    /// Exact implementation identity observed by the runner.
    pub implementation_digest: Sha256Digest,
    /// One observation for every planned action.
    pub actions: Vec<CheckpointActionObservation>,
    /// Any externally observed forbidden boundary; valid records contain none.
    pub forbidden_events: Vec<ForbiddenCheckpointBoundary>,
}

/// Fully accounted checkpoint record suitable for closure admission.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CheckpointRecord {
    /// Exact implementation identity checked.
    pub implementation_digest: Sha256Digest,
    /// Canonically ordered action observations.
    pub actions: Vec<CheckpointActionObservation>,
    /// Generated-asset decision inherited from the validated plan.
    pub generation: CheckpointGenerationDecision,
    /// Approved exception retained for sign-off, never executed locally.
    pub deferred_signoff_exception: Option<DeferredSignoffException>,
}

/// Checked generated-asset identities used to select reuse or one scoped refresh.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientCheckedAssetState {
    /// Digest of the owning semantic inputs at the current revision.
    pub owning_input_digest: Sha256Digest,
    /// Owning-input digest recorded by the checked output.
    pub checked_input_digest: Sha256Digest,
    /// Digest of the complete checked output.
    pub checked_output_digest: Sha256Digest,
}

/// Expected Cargo process count for one exact typed action.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientCargoExpectation {
    /// Exact package/target action whose process count is bounded.
    pub action: CheckpointAction,
    /// Complete Cargo invocation count, including nested fixture invocations.
    pub invocations: u32,
}

/// Generated-asset work admitted for a standalone-client checkpoint.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientAssetDisposition {
    /// Current owning inputs match the checked asset identity.
    CheckedGeneratedReused,
    /// Current owning inputs differ and authorize one bounded refresh.
    ScopedRegenerationPerformed,
}

/// Standalone-client extension of the existing engine-free checkpoint request.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientCheckpointRequest {
    /// Base typed request; arbitrary commands remain impossible.
    pub checkpoint: CheckpointRequest,
    /// Checked-asset input and output identities.
    pub asset: ClientCheckedAssetState,
    /// One explicit process-count expectation for every action.
    pub cargo: Vec<ClientCargoExpectation>,
}

/// Validated standalone-client checkpoint plan.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientCheckpointPlan {
    /// Validated base plan.
    pub checkpoint: CheckpointPlan,
    /// Checked-asset input and output identities.
    pub asset: ClientCheckedAssetState,
    /// Proven reuse or refresh disposition.
    pub disposition: ClientAssetDisposition,
    /// Canonically action-ordered Cargo process expectations.
    pub cargo: Vec<ClientCargoExpectation>,
}

/// One timed standalone-client action observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientCheckpointActionObservation {
    /// Base outcome and elapsed phase timing.
    pub action: CheckpointActionObservation,
    /// Complete observed Cargo invocation count for this phase.
    pub cargo_invocations: u32,
}

/// Complete standalone-client checkpoint observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientCheckpointObservation {
    /// Exact implementation identity observed by the runner.
    pub implementation_digest: Sha256Digest,
    /// Owning-input identity observed immediately before execution.
    pub asset_input_digest: Sha256Digest,
    /// Checked-output identity observed immediately after execution.
    pub asset_output_digest: Sha256Digest,
    /// One outcome for every exact planned action.
    pub actions: Vec<ClientCheckpointActionObservation>,
    /// Any external forbidden event; valid records contain none.
    pub forbidden_events: Vec<ForbiddenCheckpointBoundary>,
}

/// Fully accounted standalone-client checkpoint record.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientCheckpointRecord {
    /// Canonical base record.
    pub checkpoint: CheckpointRecord,
    /// Proven reuse or refresh disposition.
    pub disposition: ClientAssetDisposition,
    /// Owning-input identity admitted by the record.
    pub asset_input_digest: Sha256Digest,
    /// Checked-output identity admitted by the record.
    pub asset_output_digest: Sha256Digest,
    /// Canonically action-ordered Cargo process counts.
    pub cargo: Vec<ClientCargoExpectation>,
}

/// Closed action vocabulary for the complete conformance-model checkpoint.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(tag = "action", rename_all = "kebab-case", deny_unknown_fields)]
pub enum Feature8CheckpointAction {
    /// Verify formatting for the two private conformance packages.
    Format {
        /// Exact package closure formatted as one workspace action.
        packages: BTreeSet<CheckpointPackage>,
    },
    /// Type-check one private package with all features and the committed lockfile.
    Check {
        /// Exact package selected by Cargo.
        package: CheckpointPackage,
    },
    /// Execute an explicit set of engine-free test binaries.
    NamedTests {
        /// Exact private package which owns every target.
        package: CheckpointPackage,
        /// Closed test target inventory.
        targets: BTreeSet<CheckpointTestTarget>,
        /// Correctness properties accounted for by those targets.
        properties: BTreeSet<ModuleProperty>,
    },
    /// Exercise checked source and dependency-boundary policy.
    SourcePolicy {
        /// Exact package closure inspected by source-policy tests.
        packages: BTreeSet<CheckpointPackage>,
    },
    /// Execute the direct, engine-free Go sign-off adapter tests.
    DirectGoSignoffAdapter,
    /// Assemble native observation fixtures without running an engine.
    NativeEvidenceAggregation,
    /// Compile current child closure, catalog, platform, and security evidence identities.
    EvidenceAggregation,
    /// Run warning-denied Clippy over the exact private package closure.
    Clippy {
        /// Exact packages selected by Cargo.
        packages: BTreeSet<CheckpointPackage>,
    },
    /// Build warning-denied rustdoc without dependencies.
    Rustdoc {
        /// Exact packages selected by Cargo.
        packages: BTreeSet<CheckpointPackage>,
    },
    /// Evaluate the checked Cargo Deny policy.
    CargoDeny,
    /// Verify that unchanged checked assets still match their owning inputs.
    CheckedAssetDrift,
    /// Verify that generated evidence and the source tree have no unexplained output.
    CleanOutput,
}

/// Local checkpoint phase used for reviewed aggregate timeout accounting.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Feature8CheckpointPhase {
    /// Formatting.
    Format,
    /// Locked package checking.
    Check,
    /// Named Rust property and fixture tests.
    Test,
    /// Source-policy validation.
    SourcePolicy,
    /// Direct Go adapter validation.
    DirectGo,
    /// Native evidence assembly.
    NativeEvidence,
    /// Child and umbrella evidence assembly.
    Evidence,
    /// Clippy and Cargo Deny security/hygiene gates.
    Security,
    /// Warning-denied rustdoc.
    Documentation,
    /// Checked generated-asset identity validation.
    CheckedAssets,
    /// Clean derived-output validation.
    CleanOutput,
}

/// One exact reviewed aggregate phase budget.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8PhaseBudget {
    /// Phase receiving the budget.
    pub phase: Feature8CheckpointPhase,
    /// Positive aggregate wall-time bound.
    pub maximum_millis: u64,
}

/// Terminal result of a current or reusable checkpoint action.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Feature8ActionOutcome {
    /// Action completed successfully within its phase budget.
    Passed,
    /// Action completed with a failure.
    Failed,
    /// Runner terminated the phase at its reviewed bound.
    TimedOut,
}

/// Prior action evidence considered for change-triggered reuse.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8PriorActionObservation {
    /// Owning-input identity used by the earlier action.
    pub owning_input_digest: Sha256Digest,
    /// Earlier terminal outcome.
    pub outcome: Feature8ActionOutcome,
    /// Earlier wall time retained for evidence accounting.
    pub elapsed_millis: u64,
    /// Complete earlier Cargo process count.
    pub cargo_invocations: u32,
    /// Canonical result or observation identity.
    pub output_digest: Sha256Digest,
}

/// Current action binding plus optional reusable evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8ActionInput {
    /// Exact closed action.
    pub action: Feature8CheckpointAction,
    /// Digest of every source, fixture, and policy input owned by the action.
    pub owning_input_digest: Sha256Digest,
    /// Complete Cargo process count expected when the action executes.
    pub expected_cargo_invocations: u32,
    /// Optional prior evidence; stale or failed evidence is scheduled rather than trusted.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub prior: Option<Feature8PriorActionObservation>,
}

/// Proposal boundary retained before the closed action inventory is validated.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "proposal", rename_all = "kebab-case", deny_unknown_fields)]
pub enum Feature8CheckpointProposal {
    /// One typed engine-free action.
    Action {
        /// Exact action proposed for the checkpoint.
        action: Feature8CheckpointAction,
    },
    /// Boundary which can never enter a local checkpoint plan.
    Forbidden {
        /// Exact forbidden domain requested by the caller.
        boundary: ForbiddenCheckpointBoundary,
    },
}

/// Complete request for the final broad engine-free conformance-model checkpoint.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8CheckpointRequest {
    /// Exact focused source identity.
    pub implementation_digest: Sha256Digest,
    /// Proposed actions; valid plans equal the closed reviewed inventory.
    pub proposals: Vec<Feature8CheckpointProposal>,
    /// One binding and reuse candidate for every action.
    pub inputs: Vec<Feature8ActionInput>,
    /// Exact reviewed aggregate phase budgets.
    pub phase_budgets: Vec<Feature8PhaseBudget>,
    /// Checked generated-asset reuse or scoped-refresh decision.
    pub generation: CheckpointGenerationDecision,
}

/// Whether one current action executes or consumes matching passed evidence.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Feature8ActionDisposition {
    /// Matching current passed evidence is consumed without replay.
    Reused,
    /// Missing, failed, or stale evidence schedules the owning action.
    Execute,
}

/// Validated current action with exact input, phase, count, and reuse decision.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8PlannedAction {
    /// Exact closed action.
    pub action: Feature8CheckpointAction,
    /// Owning input which governs reuse.
    pub owning_input_digest: Sha256Digest,
    /// Aggregate budget phase.
    pub phase: Feature8CheckpointPhase,
    /// Complete expected Cargo count.
    pub expected_cargo_invocations: u32,
    /// Execute or reuse decision.
    pub disposition: Feature8ActionDisposition,
    /// Matching passed prior evidence when disposition is reuse.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reusable_observation: Option<Feature8PriorActionObservation>,
}

/// Validated checkpoint plan with no representable engine or exact-target work.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8CheckpointPlan {
    /// Exact focused source identity.
    pub implementation_digest: Sha256Digest,
    /// Actions in canonical typed order.
    pub actions: Vec<Feature8PlannedAction>,
    /// Budgets in canonical phase order.
    pub phase_budgets: Vec<Feature8PhaseBudget>,
    /// Checked generated-asset decision.
    pub generation: CheckpointGenerationDecision,
}

/// Current observation for one action selected for execution.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8ActionObservation {
    /// Exact executed action.
    pub action: Feature8CheckpointAction,
    /// Owning-input identity observed immediately before execution.
    pub owning_input_digest: Sha256Digest,
    /// Terminal outcome, including a distinct timeout result.
    pub outcome: Feature8ActionOutcome,
    /// Positive current wall time.
    pub elapsed_millis: u64,
    /// Complete current Cargo invocation count.
    pub cargo_invocations: u32,
    /// Canonical result or observation identity.
    pub output_digest: Sha256Digest,
}

/// Complete current execution observation for all scheduled actions.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8CheckpointObservation {
    /// Exact focused source identity observed by the runner.
    pub implementation_digest: Sha256Digest,
    /// One current observation for every scheduled action and none for reused actions.
    pub actions: Vec<Feature8ActionObservation>,
    /// Externally observed forbidden events; valid records contain none.
    pub forbidden_events: Vec<ForbiddenCheckpointBoundary>,
    /// Whether the local checkpoint incorrectly claimed exact SDK sign-off.
    pub sdk_signoff_claimed: bool,
}

/// Canonical retained action record independent of execute versus reuse.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8ActionRecord {
    /// Exact closed action.
    pub action: Feature8CheckpointAction,
    /// Owning input which was proved current.
    pub owning_input_digest: Sha256Digest,
    /// Execute or reuse decision.
    pub disposition: Feature8ActionDisposition,
    /// Terminal result.
    pub outcome: Feature8ActionOutcome,
    /// Accounted action wall time.
    pub elapsed_millis: u64,
    /// Accounted Cargo invocation count.
    pub cargo_invocations: u32,
    /// Canonical output identity.
    pub output_digest: Sha256Digest,
}

/// Fully accounted checkpoint record before implementation-closure admission.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Feature8CheckpointRecord {
    /// Exact focused source identity.
    pub implementation_digest: Sha256Digest,
    /// Canonically ordered action records.
    pub actions: Vec<Feature8ActionRecord>,
    /// Checked generated-asset decision.
    pub generation: CheckpointGenerationDecision,
    /// Aggregate phases which exceeded their reviewed budget.
    pub timed_out_phases: BTreeSet<Feature8CheckpointPhase>,
    /// Sum of every retained current or reusable action duration.
    pub total_elapsed_millis: u64,
    /// Sum of every retained Cargo invocation count.
    pub total_cargo_invocations: u32,
    /// True only when every required current action passed within all budgets.
    pub complete: bool,
    /// Local checkpoints always retain a false exact-sign-off claim.
    pub sdk_signoff_claimed: bool,
}

/// Validates a request into an engine-free executable plan.
pub fn plan_checkpoint(request: CheckpointRequest) -> Result<CheckpointPlan, ModuleDiagnosticSet> {
    if request.proposals.is_empty() {
        return Err(checkpoint_error(
            "checkpoint plan contains no typed Rust actions",
        ));
    }
    let mut actions = BTreeSet::new();
    for proposal in request.proposals {
        let CheckpointProposal::Action { action } = proposal else {
            return Err(checkpoint_error(
                "checkpoint plan enters a forbidden engine, Dagger, network, SDK, generation, or distribution boundary",
            ));
        };
        if !action_is_well_scoped(&action) || !actions.insert(action) {
            return Err(checkpoint_error(
                "checkpoint action is empty, duplicated, or not package scoped",
            ));
        }
    }
    if let CheckpointGenerationDecision::ScopedRefresh {
        changed_domains, ..
    } = &request.generation
        && changed_domains.is_empty()
    {
        return Err(checkpoint_error(
            "scoped regeneration requires at least one changed owning input domain",
        ));
    }
    if let Some(exception) = &request.deferred_signoff_exception
        && (!exception.approved
            || !safe_nonempty(&exception.contract_gap)
            || !safe_nonempty(&exception.model_insufficiency)
            || !safe_nonempty(&exception.proposed_case))
    {
        return Err(checkpoint_error(
            "engine exception lacks an exact gap, model proof, minimal case, or explicit approval",
        ));
    }
    Ok(CheckpointPlan {
        implementation_digest: request.implementation_digest,
        actions,
        generation: request.generation,
        deferred_signoff_exception: request.deferred_signoff_exception,
    })
}

/// Accounts for every planned action and rejects any observed forbidden boundary.
pub fn record_checkpoint(
    plan: &CheckpointPlan,
    observation: CheckpointObservation,
) -> Result<CheckpointRecord, ModuleDiagnosticSet> {
    if observation.implementation_digest != plan.implementation_digest
        || !observation.forbidden_events.is_empty()
    {
        return Err(checkpoint_error(
            "checkpoint observation has stale identity or crossed a forbidden boundary",
        ));
    }
    let mut actions = BTreeMap::new();
    for item in observation.actions {
        if item.elapsed_millis == 0
            || !plan.actions.contains(&item.action)
            || actions.insert(item.action.clone(), item).is_some()
        {
            return Err(checkpoint_error(
                "checkpoint observation is unplanned, duplicated, or lacks elapsed time",
            ));
        }
    }
    if actions.len() != plan.actions.len() {
        return Err(checkpoint_error(
            "checkpoint observation does not account for every planned action",
        ));
    }
    Ok(CheckpointRecord {
        implementation_digest: plan.implementation_digest.clone(),
        actions: actions.into_values().collect(),
        generation: plan.generation.clone(),
        deferred_signoff_exception: plan.deferred_signoff_exception.clone(),
    })
}

/// Extends the closed planner with only standalone-client Rust and direct-Go slices.
pub fn plan_client_checkpoint(
    request: ClientCheckpointRequest,
) -> Result<ClientCheckpointPlan, ModuleDiagnosticSet> {
    let checkpoint = plan_checkpoint(request.checkpoint)?;
    if !checkpoint.actions.iter().all(client_action_is_scoped) {
        return Err(checkpoint_error(
            "standalone-client checkpoint selected a package or target outside its Rust closure",
        ));
    }

    let disposition = match &checkpoint.generation {
        CheckpointGenerationDecision::ReuseChecked { manifest_digest }
            if request.asset.owning_input_digest == request.asset.checked_input_digest
                && manifest_digest == &request.asset.checked_output_digest =>
        {
            ClientAssetDisposition::CheckedGeneratedReused
        }
        CheckpointGenerationDecision::ScopedRefresh {
            changed_domains,
            manifest_digest,
        } if request.asset.owning_input_digest != request.asset.checked_input_digest
            && !changed_domains.is_empty()
            && manifest_digest == &request.asset.checked_output_digest =>
        {
            ClientAssetDisposition::ScopedRegenerationPerformed
        }
        _ => {
            return Err(checkpoint_error(
                "checked-asset identities do not justify the requested reuse or scoped refresh",
            ));
        }
    };

    let mut cargo = request.cargo;
    cargo.sort();
    if cargo.len() != checkpoint.actions.len()
        || cargo
            .windows(2)
            .any(|pair| pair[0].action == pair[1].action)
        || cargo.iter().any(|expectation| {
            !checkpoint.actions.contains(&expectation.action)
                || cargo_action(&expectation.action) != (expectation.invocations > 0)
                || expectation.invocations > 64
        })
    {
        return Err(checkpoint_error(
            "Cargo process expectations are incomplete, duplicated, or inconsistent with the typed action",
        ));
    }

    Ok(ClientCheckpointPlan {
        checkpoint,
        asset: request.asset,
        disposition,
        cargo,
    })
}

/// Records a complete passed observation with exact asset and Cargo accounting.
pub fn record_client_checkpoint(
    plan: &ClientCheckpointPlan,
    observation: ClientCheckpointObservation,
) -> Result<ClientCheckpointRecord, ModuleDiagnosticSet> {
    if observation.asset_input_digest != plan.asset.owning_input_digest
        || observation.asset_output_digest != plan.asset.checked_output_digest
    {
        return Err(checkpoint_evidence_error(
            "standalone-client checkpoint observed stale checked-asset identities",
        ));
    }
    let expected = plan
        .cargo
        .iter()
        .map(|item| (&item.action, item.invocations))
        .collect::<BTreeMap<_, _>>();
    if observation.actions.iter().any(|item| {
        item.action.outcome != CheckpointActionOutcome::Passed
            || expected.get(&item.action.action).copied() != Some(item.cargo_invocations)
    }) {
        return Err(checkpoint_evidence_error(
            "standalone-client checkpoint has a failed action or unexpected Cargo process count",
        ));
    }
    let checkpoint = record_checkpoint(
        &plan.checkpoint,
        CheckpointObservation {
            implementation_digest: observation.implementation_digest,
            actions: observation
                .actions
                .into_iter()
                .map(|item| item.action)
                .collect(),
            forbidden_events: observation.forbidden_events,
        },
    )?;
    Ok(ClientCheckpointRecord {
        checkpoint,
        disposition: plan.disposition,
        asset_input_digest: plan.asset.owning_input_digest.clone(),
        asset_output_digest: plan.asset.checked_output_digest.clone(),
        cargo: plan.cargo.clone(),
    })
}

/// Returns the complete typed action inventory for the broad conformance-model checkpoint.
#[must_use]
pub fn feature8_checkpoint_actions() -> BTreeSet<Feature8CheckpointAction> {
    let packages = BTreeSet::from([
        CheckpointPackage::DaggerSdkEngine,
        CheckpointPackage::DaggerSdkCompleteness,
    ]);
    BTreeSet::from([
        Feature8CheckpointAction::Format {
            packages: packages.clone(),
        },
        Feature8CheckpointAction::Check {
            package: CheckpointPackage::DaggerSdkEngine,
        },
        Feature8CheckpointAction::Check {
            package: CheckpointPackage::DaggerSdkCompleteness,
        },
        feature8_test_action(
            CheckpointPackage::DaggerSdkCompleteness,
            &[
                "conformance_applicability_properties",
                "conformance_catalog",
                "conformance_closure",
                "conformance_foundation",
                "conformance_observable_properties",
                "conformance_scenario_runner_compile",
                "platform_matrix_properties",
                "signoff_artifact_properties",
                "signoff_execution_properties",
                "signoff_preflight_properties",
                "signoff_security_properties",
                "signoff_verdict_properties",
            ],
            &(1_u8..=20).chain(23_u8..=24).collect::<Vec<_>>(),
        ),
        feature8_test_action(
            CheckpointPackage::DaggerSdkEngine,
            &["conformance_checkpoint_properties"],
            &[21, 22],
        ),
        Feature8CheckpointAction::SourcePolicy {
            packages: packages.clone(),
        },
        Feature8CheckpointAction::DirectGoSignoffAdapter,
        Feature8CheckpointAction::NativeEvidenceAggregation,
        Feature8CheckpointAction::EvidenceAggregation,
        Feature8CheckpointAction::Clippy {
            packages: packages.clone(),
        },
        Feature8CheckpointAction::Rustdoc { packages },
        Feature8CheckpointAction::CargoDeny,
        Feature8CheckpointAction::CheckedAssetDrift,
        Feature8CheckpointAction::CleanOutput,
    ])
}

/// Returns the reviewed aggregate wall-time budget for every checkpoint phase.
#[must_use]
pub fn feature8_checkpoint_phase_budgets() -> Vec<Feature8PhaseBudget> {
    use Feature8CheckpointPhase as Phase;
    [
        (Phase::Format, 60_000),
        (Phase::Check, 300_000),
        (Phase::Test, 900_000),
        (Phase::SourcePolicy, 300_000),
        (Phase::DirectGo, 120_000),
        (Phase::NativeEvidence, 300_000),
        (Phase::Evidence, 300_000),
        (Phase::Security, 600_000),
        (Phase::Documentation, 300_000),
        (Phase::CheckedAssets, 120_000),
        (Phase::CleanOutput, 120_000),
    ]
    .into_iter()
    .map(|(phase, maximum_millis)| Feature8PhaseBudget {
        phase,
        maximum_millis,
    })
    .collect()
}

/// Compiles the exact change-triggered checkpoint plan without rendering commands.
pub fn plan_feature8_checkpoint(
    request: Feature8CheckpointRequest,
) -> Result<Feature8CheckpointPlan, ModuleDiagnosticSet> {
    let required = feature8_checkpoint_actions();
    let mut proposed = BTreeSet::new();
    for proposal in request.proposals {
        let Feature8CheckpointProposal::Action { action } = proposal else {
            return Err(feature8_checkpoint_error(
                "conformance checkpoint proposed a forbidden execution boundary",
            ));
        };
        if !feature8_action_is_scoped(&action) || !proposed.insert(action) {
            return Err(feature8_checkpoint_error(
                "conformance checkpoint action is duplicated empty or outside Rust scope",
            ));
        }
    }
    if proposed != required {
        return Err(feature8_checkpoint_error(
            "conformance checkpoint action inventory is incomplete or widened",
        ));
    }

    let expected_budgets = feature8_budget_map(&feature8_checkpoint_phase_budgets())?;
    let observed_budgets = feature8_budget_map(&request.phase_budgets)?;
    if observed_budgets != expected_budgets {
        return Err(feature8_checkpoint_error(
            "conformance checkpoint phase budgets differ from reviewed policy",
        ));
    }
    if let CheckpointGenerationDecision::ScopedRefresh {
        changed_domains, ..
    } = &request.generation
        && changed_domains.is_empty()
    {
        return Err(feature8_checkpoint_error(
            "scoped asset refresh lacks a changed owning input",
        ));
    }

    let mut inputs = BTreeMap::new();
    for input in request.inputs {
        let expected_cargo = feature8_expected_cargo_invocations(&input.action);
        if !required.contains(&input.action)
            || input.expected_cargo_invocations != expected_cargo
            || inputs.insert(input.action.clone(), input).is_some()
        {
            return Err(feature8_checkpoint_error(
                "conformance checkpoint input binding is missing duplicated or miscounted",
            ));
        }
    }
    if inputs.len() != required.len() {
        return Err(feature8_checkpoint_error(
            "conformance checkpoint does not bind every required action input",
        ));
    }

    let mut actions = Vec::with_capacity(required.len());
    for action in required {
        let input = inputs
            .remove(&action)
            .expect("exact action set has an input after cardinality validation");
        let phase = feature8_action_phase(&action);
        let phase_budget = expected_budgets
            .get(&phase)
            .copied()
            .expect("every closed action phase has a reviewed budget");
        let reusable = input.prior.filter(|prior| {
            prior.owning_input_digest == input.owning_input_digest
                && prior.outcome == Feature8ActionOutcome::Passed
                && prior.elapsed_millis > 0
                && prior.elapsed_millis <= phase_budget
                && prior.cargo_invocations == input.expected_cargo_invocations
        });
        actions.push(Feature8PlannedAction {
            action,
            owning_input_digest: input.owning_input_digest,
            phase,
            expected_cargo_invocations: input.expected_cargo_invocations,
            disposition: if reusable.is_some() {
                Feature8ActionDisposition::Reused
            } else {
                Feature8ActionDisposition::Execute
            },
            reusable_observation: reusable,
        });
    }
    Ok(Feature8CheckpointPlan {
        implementation_digest: request.implementation_digest,
        actions,
        phase_budgets: feature8_checkpoint_phase_budgets(),
        generation: request.generation,
    })
}

/// Accounts for reused and executed actions while retaining failures and timeout phases.
pub fn record_feature8_checkpoint(
    plan: &Feature8CheckpointPlan,
    observation: Feature8CheckpointObservation,
) -> Result<Feature8CheckpointRecord, ModuleDiagnosticSet> {
    if observation.implementation_digest != plan.implementation_digest
        || !observation.forbidden_events.is_empty()
        || observation.sdk_signoff_claimed
    {
        return Err(feature8_evidence_error(
            "conformance checkpoint observed stale identity forbidden work or a sign-off claim",
        ));
    }
    let budgets = feature8_budget_map(&plan.phase_budgets)?;
    let mut current = BTreeMap::new();
    for item in observation.actions {
        if current.insert(item.action.clone(), item).is_some() {
            return Err(feature8_evidence_error(
                "conformance checkpoint action observation is duplicated",
            ));
        }
    }

    let mut records = Vec::with_capacity(plan.actions.len());
    for planned in &plan.actions {
        let record = match planned.disposition {
            Feature8ActionDisposition::Reused => {
                let prior = planned.reusable_observation.as_ref().ok_or_else(|| {
                    feature8_evidence_error(
                        "reused conformance action has no matching passed observation",
                    )
                })?;
                if current.contains_key(&planned.action) {
                    return Err(feature8_evidence_error(
                        "reused conformance action was executed again",
                    ));
                }
                Feature8ActionRecord {
                    action: planned.action.clone(),
                    owning_input_digest: planned.owning_input_digest.clone(),
                    disposition: Feature8ActionDisposition::Reused,
                    outcome: prior.outcome,
                    elapsed_millis: prior.elapsed_millis,
                    cargo_invocations: prior.cargo_invocations,
                    output_digest: prior.output_digest.clone(),
                }
            }
            Feature8ActionDisposition::Execute => {
                let item = current.remove(&planned.action).ok_or_else(|| {
                    feature8_evidence_error(
                        "scheduled conformance action has no current observation",
                    )
                })?;
                let budget = budgets
                    .get(&planned.phase)
                    .copied()
                    .expect("validated phase has a budget");
                let timing_is_valid = item.elapsed_millis > 0
                    && match item.outcome {
                        Feature8ActionOutcome::TimedOut => item.elapsed_millis >= budget,
                        Feature8ActionOutcome::Passed | Feature8ActionOutcome::Failed => {
                            item.elapsed_millis <= budget
                        }
                    };
                if item.owning_input_digest != planned.owning_input_digest
                    || item.cargo_invocations != planned.expected_cargo_invocations
                    || !timing_is_valid
                {
                    return Err(feature8_evidence_error(
                        "current conformance action is stale miscounted or has invalid timing",
                    ));
                }
                Feature8ActionRecord {
                    action: item.action,
                    owning_input_digest: item.owning_input_digest,
                    disposition: Feature8ActionDisposition::Execute,
                    outcome: item.outcome,
                    elapsed_millis: item.elapsed_millis,
                    cargo_invocations: item.cargo_invocations,
                    output_digest: item.output_digest,
                }
            }
        };
        records.push(record);
    }
    if !current.is_empty() {
        return Err(feature8_evidence_error(
            "conformance checkpoint observed an unplanned action",
        ));
    }

    let mut phase_elapsed = BTreeMap::<Feature8CheckpointPhase, u64>::new();
    let mut total_elapsed_millis = 0_u64;
    let mut total_cargo_invocations = 0_u32;
    for (planned, record) in plan.actions.iter().zip(&records) {
        *phase_elapsed.entry(planned.phase).or_default() = phase_elapsed
            .get(&planned.phase)
            .copied()
            .unwrap_or_default()
            .checked_add(record.elapsed_millis)
            .ok_or_else(|| feature8_evidence_error("checkpoint phase timing overflowed"))?;
        total_elapsed_millis = total_elapsed_millis
            .checked_add(record.elapsed_millis)
            .ok_or_else(|| feature8_evidence_error("checkpoint total timing overflowed"))?;
        total_cargo_invocations = total_cargo_invocations
            .checked_add(record.cargo_invocations)
            .ok_or_else(|| feature8_evidence_error("checkpoint Cargo count overflowed"))?;
    }
    let timed_out_phases = plan
        .actions
        .iter()
        .zip(&records)
        .filter_map(|(planned, record)| {
            let phase_over = phase_elapsed
                .get(&planned.phase)
                .is_some_and(|elapsed| *elapsed > budgets[&planned.phase]);
            (record.outcome == Feature8ActionOutcome::TimedOut || phase_over)
                .then_some(planned.phase)
        })
        .collect::<BTreeSet<_>>();
    let complete = timed_out_phases.is_empty()
        && records
            .iter()
            .all(|record| record.outcome == Feature8ActionOutcome::Passed);
    Ok(Feature8CheckpointRecord {
        implementation_digest: plan.implementation_digest.clone(),
        actions: records,
        generation: plan.generation.clone(),
        timed_out_phases,
        total_elapsed_millis,
        total_cargo_invocations,
        complete,
        sdk_signoff_claimed: false,
    })
}

/// Admits only a complete, bounded, non-sign-off checkpoint as implementation closure.
pub fn admit_feature8_checkpoint_closure(
    record: &Feature8CheckpointRecord,
) -> Result<Sha256Digest, ModuleDiagnosticSet> {
    let required = feature8_checkpoint_actions();
    let observed = record
        .actions
        .iter()
        .map(|item| item.action.clone())
        .collect::<BTreeSet<_>>();
    let elapsed = record
        .actions
        .iter()
        .try_fold(0_u64, |total, item| total.checked_add(item.elapsed_millis));
    let cargo = record.actions.iter().try_fold(0_u32, |total, item| {
        total.checked_add(item.cargo_invocations)
    });
    if !record.complete
        || record.sdk_signoff_claimed
        || !record.timed_out_phases.is_empty()
        || observed != required
        || record
            .actions
            .iter()
            .any(|item| item.outcome != Feature8ActionOutcome::Passed)
        || elapsed != Some(record.total_elapsed_millis)
        || cargo != Some(record.total_cargo_invocations)
    {
        return Err(feature8_evidence_error(
            "conformance checkpoint evidence is incomplete stale or overbroad",
        ));
    }
    canonical_digest(DigestDomain::ConformanceCheckpoint, record).map_err(|_| {
        feature8_evidence_error("conformance checkpoint record cannot be encoded canonically")
    })
}

/// Returns the complete typed standalone-client feature-end action inventory.
///
/// The planner may reuse a current observation for any item, but this closed set is
/// the authority for what must be accounted before implementation closure.
#[must_use]
pub fn client_feature_end_checkpoint_actions() -> BTreeSet<CheckpointAction> {
    let packages = BTreeSet::from([
        CheckpointPackage::DaggerCodegen,
        CheckpointPackage::DaggerSdk,
        CheckpointPackage::DaggerSdkEngine,
        CheckpointPackage::DaggerSdkCompleteness,
    ]);
    BTreeSet::from([
        CheckpointAction::Format {
            packages: packages.clone(),
        },
        CheckpointAction::Check {
            package: CheckpointPackage::DaggerCodegen,
            all_features: true,
        },
        CheckpointAction::Check {
            package: CheckpointPackage::DaggerSdk,
            all_features: true,
        },
        CheckpointAction::Check {
            package: CheckpointPackage::DaggerSdkEngine,
            all_features: true,
        },
        CheckpointAction::Check {
            package: CheckpointPackage::DaggerSdkCompleteness,
            all_features: true,
        },
        client_test_action(
            CheckpointPackage::DaggerCodegen,
            &[
                "client_compiler_properties",
                "client_metadata_properties",
                "client_renderer",
                "client_source_policy",
                "visible_schema_properties",
            ],
            &[5, 6, 8, 10, 22],
        ),
        client_test_action(
            CheckpointPackage::DaggerSdk,
            &[
                "generated_client_compile",
                "generated_client_query_properties",
                "source_policy",
            ],
            &[7, 9, 24],
        ),
        client_test_action(
            CheckpointPackage::DaggerSdkEngine,
            &[
                "client_checkpoint_properties",
                "client_diagnostic_properties",
                "client_project_properties",
                "client_usability_properties",
                "workspace_client_properties",
            ],
            &[2, 3, 4, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 23, 25],
        ),
        client_test_action(
            CheckpointPackage::DaggerSdkCompleteness,
            &[
                "client_generation_documentation",
                "client_generation_evidence",
                "client_generation_scope",
                "initial_baseline",
            ],
            &[1, 26, 27],
        ),
        CheckpointAction::Clippy {
            packages: packages.clone(),
        },
        CheckpointAction::Rustdoc { packages },
        CheckpointAction::CargoDeny,
        CheckpointAction::RepositoryRustSecurity,
        CheckpointAction::GeneratedAssetDrift,
        CheckpointAction::PackageContents {
            package: PublicCheckpointPackage::DaggerSdk,
        },
        CheckpointAction::PackageContents {
            package: PublicCheckpointPackage::DaggerSdkMacros,
        },
        CheckpointAction::DirectGoAbi {
            package: RustGoAbiPackage::Runtime,
        },
        CheckpointAction::CleanOutput,
    ])
}

fn client_test_action(
    package: CheckpointPackage,
    targets: &[&str],
    properties: &[u8],
) -> CheckpointAction {
    CheckpointAction::Test {
        package,
        targets: targets
            .iter()
            .map(|target| {
                CheckpointTestTarget::new(*target)
                    .expect("reviewed client test target spelling is valid")
            })
            .collect(),
        properties: properties
            .iter()
            .map(|property| {
                ModuleProperty::new(*property)
                    .expect("reviewed client property identity is in range")
            })
            .collect(),
    }
}

fn feature8_test_action(
    package: CheckpointPackage,
    targets: &[&str],
    properties: &[u8],
) -> Feature8CheckpointAction {
    Feature8CheckpointAction::NamedTests {
        package,
        targets: targets
            .iter()
            .map(|target| {
                CheckpointTestTarget::new(*target)
                    .expect("reviewed conformance test target spelling is valid")
            })
            .collect(),
        properties: properties
            .iter()
            .map(|property| {
                ModuleProperty::new(*property)
                    .expect("reviewed conformance property identity is in range")
            })
            .collect(),
    }
}

fn feature8_budget_map(
    budgets: &[Feature8PhaseBudget],
) -> Result<BTreeMap<Feature8CheckpointPhase, u64>, ModuleDiagnosticSet> {
    let mut mapped = BTreeMap::new();
    for budget in budgets {
        if budget.maximum_millis == 0
            || mapped.insert(budget.phase, budget.maximum_millis).is_some()
        {
            return Err(feature8_checkpoint_error(
                "conformance checkpoint phase budget is zero or duplicated",
            ));
        }
    }
    Ok(mapped)
}

fn feature8_action_is_scoped(action: &Feature8CheckpointAction) -> bool {
    let package_is_owned = |package: &CheckpointPackage| {
        matches!(
            package,
            CheckpointPackage::DaggerSdkEngine | CheckpointPackage::DaggerSdkCompleteness
        )
    };
    match action {
        Feature8CheckpointAction::Format { packages }
        | Feature8CheckpointAction::SourcePolicy { packages }
        | Feature8CheckpointAction::Clippy { packages }
        | Feature8CheckpointAction::Rustdoc { packages } => {
            !packages.is_empty() && packages.iter().all(package_is_owned)
        }
        Feature8CheckpointAction::Check { package } => package_is_owned(package),
        Feature8CheckpointAction::NamedTests {
            package,
            targets,
            properties,
        } => package_is_owned(package) && !targets.is_empty() && !properties.is_empty(),
        Feature8CheckpointAction::DirectGoSignoffAdapter
        | Feature8CheckpointAction::NativeEvidenceAggregation
        | Feature8CheckpointAction::EvidenceAggregation
        | Feature8CheckpointAction::CargoDeny
        | Feature8CheckpointAction::CheckedAssetDrift
        | Feature8CheckpointAction::CleanOutput => true,
    }
}

const fn feature8_action_phase(action: &Feature8CheckpointAction) -> Feature8CheckpointPhase {
    match action {
        Feature8CheckpointAction::Format { .. } => Feature8CheckpointPhase::Format,
        Feature8CheckpointAction::Check { .. } => Feature8CheckpointPhase::Check,
        Feature8CheckpointAction::NamedTests { .. } => Feature8CheckpointPhase::Test,
        Feature8CheckpointAction::SourcePolicy { .. } => Feature8CheckpointPhase::SourcePolicy,
        Feature8CheckpointAction::DirectGoSignoffAdapter => Feature8CheckpointPhase::DirectGo,
        Feature8CheckpointAction::NativeEvidenceAggregation => {
            Feature8CheckpointPhase::NativeEvidence
        }
        Feature8CheckpointAction::EvidenceAggregation => Feature8CheckpointPhase::Evidence,
        Feature8CheckpointAction::Clippy { .. } | Feature8CheckpointAction::CargoDeny => {
            Feature8CheckpointPhase::Security
        }
        Feature8CheckpointAction::Rustdoc { .. } => Feature8CheckpointPhase::Documentation,
        Feature8CheckpointAction::CheckedAssetDrift => Feature8CheckpointPhase::CheckedAssets,
        Feature8CheckpointAction::CleanOutput => Feature8CheckpointPhase::CleanOutput,
    }
}

const fn feature8_expected_cargo_invocations(action: &Feature8CheckpointAction) -> u32 {
    match action {
        Feature8CheckpointAction::DirectGoSignoffAdapter
        | Feature8CheckpointAction::CleanOutput => 0,
        Feature8CheckpointAction::Format { .. }
        | Feature8CheckpointAction::Check { .. }
        | Feature8CheckpointAction::NamedTests { .. }
        | Feature8CheckpointAction::SourcePolicy { .. }
        | Feature8CheckpointAction::NativeEvidenceAggregation
        | Feature8CheckpointAction::EvidenceAggregation
        | Feature8CheckpointAction::Clippy { .. }
        | Feature8CheckpointAction::Rustdoc { .. }
        | Feature8CheckpointAction::CargoDeny
        | Feature8CheckpointAction::CheckedAssetDrift => 1,
    }
}

fn action_is_well_scoped(action: &CheckpointAction) -> bool {
    match action {
        CheckpointAction::Format { packages }
        | CheckpointAction::Clippy { packages }
        | CheckpointAction::Rustdoc { packages } => !packages.is_empty(),
        CheckpointAction::Check { .. }
        | CheckpointAction::Test { .. }
        | CheckpointAction::CargoDeny
        | CheckpointAction::RepositoryRustSecurity
        | CheckpointAction::GeneratedAssetDrift
        | CheckpointAction::PackageContents { .. }
        | CheckpointAction::DirectGoAbi { .. }
        | CheckpointAction::CleanOutput => true,
    }
}

fn client_action_is_scoped(action: &CheckpointAction) -> bool {
    let package_is_client_owned = |package: &CheckpointPackage| {
        matches!(
            package,
            CheckpointPackage::DaggerCodegen
                | CheckpointPackage::DaggerSdk
                | CheckpointPackage::DaggerSdkEngine
                | CheckpointPackage::DaggerSdkCompleteness
        )
    };
    match action {
        CheckpointAction::Format { packages }
        | CheckpointAction::Clippy { packages }
        | CheckpointAction::Rustdoc { packages } => {
            !packages.is_empty() && packages.iter().all(package_is_client_owned)
        }
        CheckpointAction::Check { package, .. } | CheckpointAction::Test { package, .. } => {
            package_is_client_owned(package)
        }
        CheckpointAction::PackageContents { package } => matches!(
            package,
            PublicCheckpointPackage::DaggerSdk | PublicCheckpointPackage::DaggerSdkMacros
        ),
        CheckpointAction::CargoDeny
        | CheckpointAction::RepositoryRustSecurity
        | CheckpointAction::GeneratedAssetDrift
        | CheckpointAction::DirectGoAbi { .. }
        | CheckpointAction::CleanOutput => true,
    }
}

fn cargo_action(action: &CheckpointAction) -> bool {
    matches!(
        action,
        CheckpointAction::Format { .. }
            | CheckpointAction::Check { .. }
            | CheckpointAction::Test { .. }
            | CheckpointAction::Clippy { .. }
            | CheckpointAction::Rustdoc { .. }
            | CheckpointAction::CargoDeny
            | CheckpointAction::RepositoryRustSecurity
            | CheckpointAction::GeneratedAssetDrift
            | CheckpointAction::PackageContents { .. }
    )
}

fn safe_nonempty(value: &str) -> bool {
    !value.is_empty()
        && value.trim() == value
        && value.len() <= 512
        && !value.chars().any(char::is_control)
}

fn checkpoint_error(message: &'static str) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new([ModuleDiagnostic::new(
        ModuleDiagnosticCode::CheckpointScopeInvalid,
        None,
        message,
        "use only typed Rust-owned actions and defer exact-engine observations to SDK sign-off",
    )
    .expect("static checkpoint diagnostics satisfy the safe renderer policy")])
    .expect("a singleton checkpoint diagnostic set is non-empty")
}

fn checkpoint_evidence_error(message: &'static str) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new([ModuleDiagnostic::new(
        ModuleDiagnosticCode::ModuleEvidenceRejected,
        None,
        message,
        "record every planned passed action with matching asset identities, Cargo counts, and elapsed timings",
    )
    .expect("static checkpoint diagnostics satisfy the safe renderer policy")])
    .expect("a singleton checkpoint diagnostic set is non-empty")
}

fn feature8_checkpoint_error(message: &'static str) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new([ModuleDiagnostic::new(
        ModuleDiagnosticCode::CheckpointScopeInvalid,
        None,
        message,
        "use only the reviewed Rust conformance checkpoint action and phase inventory",
    )
    .expect("static conformance checkpoint diagnostics satisfy the safe renderer policy")])
    .expect("a singleton conformance checkpoint diagnostic set is non-empty")
}

fn feature8_evidence_error(message: &'static str) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new([ModuleDiagnostic::new(
        ModuleDiagnosticCode::ModuleEvidenceRejected,
        None,
        message,
        "record current bounded action outcomes counts timings reuse decisions and no sign-off claim",
    )
    .expect("static conformance evidence diagnostics satisfy the safe renderer policy")])
    .expect("a singleton conformance evidence diagnostic set is non-empty")
}
