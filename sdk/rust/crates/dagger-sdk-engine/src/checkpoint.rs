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
