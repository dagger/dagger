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
    /// A network-backed build graph.
    NetworkGraph,
    /// An unrelated language SDK builder, test, or generator.
    OtherSdk,
    /// An unscoped repository workspace generator.
    UnscopedGeneration,
    /// A distribution-wide build path.
    Distribution,
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
        CheckpointAction::CargoDeny
        | CheckpointAction::RepositoryRustSecurity
        | CheckpointAction::GeneratedAssetDrift
        | CheckpointAction::DirectGoAbi { .. }
        | CheckpointAction::CleanOutput => true,
        CheckpointAction::PackageContents { .. } => false,
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
