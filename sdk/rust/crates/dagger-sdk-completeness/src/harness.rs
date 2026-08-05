//! Bounded `sdk-sdk` mapping, evidence admission, execution, and Rust extensions.
//!
//! A harness result is never a free-standing completeness claim. It is admitted only through one
//! pinned mapping that binds the check's source fingerprint, target, CLI bytes, verified Rust
//! artifact, platform, assertion, and exact capability set. Expected subject failures become
//! blocker observations; they do not corrupt contract integrity.

use std::collections::BTreeMap;
use std::fs;
use std::ops::Deref;
use std::path::{Path, PathBuf};
use std::process::Command;

use crate::canonical::canonical_bytes;
use crate::command::{CommandPolicy, command_defects};
use crate::diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, ToolError, Validation,
};
use crate::model::{
    CanonicalInventory, CanonicalSet, CapabilityId, CheckId, CheckOutcome, CommandSpec, CommitSha,
    ConformanceScenario, Digest, EvidenceKind, EvidenceRegistry, HarnessCheckKind,
    HarnessCheckMapping, HarnessCheckResult, HarnessMappings, Platform, SourceItemInventory,
    SourceItemState, SourceLocator, TargetDigest,
};

#[derive(Clone, Debug, Eq, PartialEq)]
/// Extracted identity and semantics of one public harness check.
pub struct HarnessCheckSource {
    pub check_id: CheckId,
    pub check_kind: HarnessCheckKind,
    pub harness_revision: CommitSha,
    pub source_locator: SourceLocator,
    pub source_fingerprint: Digest,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Complete pinned public check inventory, keyed by stable identity.
pub struct HarnessCheckInventory {
    pub checks: BTreeMap<CheckId, HarnessCheckSource>,
}

/// Projects extracted `harness-check` SourceItems into the mapping authority inventory.
///
/// Check kind and fingerprint come from pinned extraction output; callers cannot independently
/// relabel a harness-self check or substitute a friendlier semantic identity while mapping it.
pub fn build_harness_check_inventory(
    source_items: &SourceItemInventory,
    harness_revision: &CommitSha,
) -> Validation<HarnessCheckInventory> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut checks = BTreeMap::new();
    for item in source_items
        .items
        .values()
        .filter(|item| item.item_kind.as_str() == "harness-check")
    {
        let check_id = item
            .semantic_signature
            .get("check_id")
            .and_then(serde_json::Value::as_str)
            .and_then(|value| CheckId::new(value).ok());
        let Some(check_id) = check_id else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::HarnessCheckMissing,
                item.source_item_id.to_string(),
                Some(item.locator.clone()),
                "extracted harness check has no canonical semantic check_id",
            ));
            continue;
        };
        let check_kind = match item.state {
            SourceItemState::Active | SourceItemState::Deprecated => {
                HarnessCheckKind::SubjectConformance
            }
            SourceItemState::HarnessSelf => HarnessCheckKind::HarnessSelf,
            SourceItemState::Skipped | SourceItemState::Removed => {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::HarnessCheckKindInvalid,
                    check_id.to_string(),
                    Some(item.locator.clone()),
                    "skipped or removed source cannot enter the active public check inventory",
                ));
                continue;
            }
        };
        let source = HarnessCheckSource {
            check_id: check_id.clone(),
            check_kind,
            harness_revision: harness_revision.clone(),
            source_locator: item.locator.clone(),
            source_fingerprint: item.fingerprint.clone(),
        };
        if checks.insert(check_id.clone(), source).is_some() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::HarnessCheckDuplicate,
                check_id.to_string(),
                Some(item.locator.clone()),
                "multiple extracted SourceItems resolve to one public check identity",
            ));
        }
    }
    diagnostics.finish(HarnessCheckInventory { checks })
}

/// Exact target and execution identities all mappings must bind.
pub struct HarnessMappingContext<'a> {
    pub harness_revision: &'a CommitSha,
    pub target: &'a TargetDigest,
    pub cli_artifact_digest: &'a Digest,
    pub verified_artifact_digest: &'a Digest,
    pub command_policy: &'a CommandPolicy,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Harness mappings whose inventory partition and all containment fields are valid.
pub struct ValidatedHarnessMappings(HarnessMappings);

impl Deref for ValidatedHarnessMappings {
    type Target = HarnessMappings;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

/// Requires one complete mapping per pinned public check, no extras, and no semantic drift.
pub fn validate_harness_mappings(
    mappings: HarnessMappings,
    checks: &HarnessCheckInventory,
    inventory: &CanonicalInventory,
    evidence: &EvidenceRegistry,
    context: &HarnessMappingContext<'_>,
) -> Validation<ValidatedHarnessMappings> {
    let mut diagnostics = DiagnosticCollector::default();
    for (check_id, source) in &checks.checks {
        let Some(mapping) = mappings.checks.get(check_id) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::HarnessCheckMissing,
                check_id.to_string(),
                Some(source.source_locator.clone()),
                "pinned public harness check has no mapping",
            ));
            continue;
        };
        validate_mapping(
            mapping,
            source,
            inventory,
            evidence,
            context,
            &mut diagnostics,
        );
    }
    for (map_id, mapping) in &mappings.checks {
        if map_id != &mapping.check_id {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::HarnessCheckDuplicate,
                mapping.check_id.to_string(),
                Some(mapping.source_locator.clone()),
                "mapping key and embedded check identity differ",
            ));
        }
        if !checks.checks.contains_key(map_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::HarnessCheckMissing,
                map_id.to_string(),
                Some(mapping.source_locator.clone()),
                "mapping names no pinned public harness check",
            ));
        }
    }
    diagnostics.finish(ValidatedHarnessMappings(mappings))
}

fn validate_mapping(
    mapping: &HarnessCheckMapping,
    source: &HarnessCheckSource,
    inventory: &CanonicalInventory,
    evidence: &EvidenceRegistry,
    context: &HarnessMappingContext<'_>,
    diagnostics: &mut DiagnosticCollector,
) {
    if mapping.check_id != source.check_id || mapping.check_kind != source.check_kind {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessCheckKindInvalid,
            mapping,
            "mapping identity or subject/self classification differs from pinned source",
        ));
    }
    if &mapping.harness_revision != context.harness_revision
        || mapping.harness_revision != source.harness_revision
    {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessRevisionMismatch,
            mapping,
            "mapping revision differs from the target and extracted check",
        ));
    }
    if mapping.source_locator != source.source_locator {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessCheckMissing,
            mapping,
            "mapping source locator differs from the extracted public check symbol",
        ));
    }
    if mapping.source_fingerprint != source.source_fingerprint {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessCheckMissing,
            mapping,
            "public check semantics changed at the mapped identity",
        ));
    }

    let partition_valid = match mapping.check_kind {
        HarnessCheckKind::SubjectConformance => !mapping.capability_ids.is_empty(),
        HarnessCheckKind::HarnessSelf => mapping.capability_ids.is_empty(),
    };
    if !partition_valid
        || mapping
            .capability_ids
            .iter()
            .any(|id| !inventory.capabilities.contains_key(id))
    {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessCapabilityMissing,
            mapping,
            "subject checks require known non-empty capabilities; harness-self requires none",
        ));
    }
    if &mapping.execution_target != context.target
        || &mapping.cli_artifact_digest != context.cli_artifact_digest
        || &mapping.verified_artifact_digest != context.verified_artifact_digest
    {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessTargetMismatch,
            mapping,
            "mapping does not bind the selected engine, CLI bytes, and Rust artifact",
        ));
    }
    if mapping.platform_scope.is_empty() {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessPlatformInvalid,
            mapping,
            "mapping requires an exact non-empty platform scope",
        ));
    }
    if !is_public_check_invocation(&mapping.invocation, &mapping.check_id) {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessInvocationInvalid,
            mapping,
            "mapping must invoke `dagger check <check-id> --no-generate` directly",
        ));
    }
    for detail in command_defects(&mapping.invocation, context.command_policy) {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessInvocationInvalid,
            mapping,
            detail,
        ));
    }
    if mapping.expected_outcome.outcome != CheckOutcome::Passed {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessOutcomeMissing,
            mapping,
            "mapping must state the passing assertion that would prove its bounded scope",
        ));
    }
    if mapping.limitations.is_empty() {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessScopeInvalid,
            mapping,
            "mapping must state behaviours and platforms it does not prove",
        ));
    }
    if let Some(evidence_id) = &mapping.verification_evidence {
        let valid = evidence.evidence.get(evidence_id).is_some_and(|reference| {
            reference.evidence_kind == EvidenceKind::Verification
                && reference.proved_capability_ids == mapping.capability_ids
                && reference.execution_target.as_ref() == Some(&mapping.execution_target)
                && mapping
                    .platform_scope
                    .iter()
                    .all(|platform| reference.platform_scope.contains(platform))
        });
        if !valid {
            diagnostics.push(mapping_diagnostic(
                DiagnosticCode::HarnessEvidenceInvalid,
                mapping,
                "optional verification evidence does not exactly cover the mapping",
            ));
        }
    }
}

fn is_public_check_invocation(command: &CommandSpec, check_id: &CheckId) -> bool {
    command.program.as_str() == "dagger"
        && command.args
            == [
                "check".to_owned(),
                check_id.to_string(),
                "--no-generate".to_owned(),
            ]
}

fn mapping_diagnostic(
    code: DiagnosticCode,
    mapping: &HarnessCheckMapping,
    detail: impl Into<String>,
) -> ContractDiagnostic {
    ContractDiagnostic::new(
        code,
        mapping.check_id.to_string(),
        Some(mapping.source_locator.clone()),
        detail,
    )
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Evidential effect of a structurally contained harness result.
pub enum HarnessAdmission {
    /// Passing evidence for exactly these mapped capabilities.
    Passing(CanonicalSet<CapabilityId>),
    /// Expected current subject incompleteness; blocks capabilities but not Integrity.
    ExpectedBlocker {
        capability_ids: CanonicalSet<CapabilityId>,
        outcome: CheckOutcome,
    },
}

/// Admits a result only inside every identity and assertion boundary in its mapping.
pub fn admit_harness_result(
    mapping: &HarnessCheckMapping,
    result: &HarnessCheckResult,
) -> Validation<HarnessAdmission> {
    let mut diagnostics = DiagnosticCollector::default();
    if result.check_id != mapping.check_id || result.check_kind != mapping.check_kind {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessCheckKindInvalid,
            mapping,
            "result check identity or subject/self kind differs from mapping",
        ));
    }
    if result.harness_revision != mapping.harness_revision {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessRevisionMismatch,
            mapping,
            "result harness revision differs from mapping",
        ));
    }
    if result.target != mapping.execution_target
        || result.cli_artifact_digest != mapping.cli_artifact_digest
        || result.verified_artifact_digest != mapping.verified_artifact_digest
    {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessTargetMismatch,
            mapping,
            "result engine, CLI bytes, or verified Rust artifact differs from mapping",
        ));
    }
    if !mapping.platform_scope.contains(&result.platform) {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessPlatformInvalid,
            mapping,
            "result platform is outside the mapping scope",
        ));
    }
    if result.assertion != mapping.expected_outcome.assertion {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessOutcomeMissing,
            mapping,
            "result assertion differs from the mapped pass condition",
        ));
    }
    if result.capability_ids != mapping.capability_ids {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessScopeInvalid,
            mapping,
            "result capability scope differs from the mapping",
        ));
    }
    if mapping.check_kind == HarnessCheckKind::SubjectConformance
        && mapping.capability_ids.is_empty()
    {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessCapabilityMissing,
            mapping,
            "subject result mapping has no capability scope",
        ));
    }
    if mapping.expected_outcome.outcome != CheckOutcome::Passed {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessOutcomeMissing,
            mapping,
            "only the mapped passing outcome is eligible for Rust completion evidence",
        ));
    }
    if mapping.check_kind == HarnessCheckKind::HarnessSelf {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessScopeInvalid,
            mapping,
            "harness-self results cannot be offered as Rust capability evidence",
        ));
    }

    let admission = if result.outcome == mapping.expected_outcome.outcome {
        HarnessAdmission::Passing(mapping.capability_ids.clone())
    } else {
        HarnessAdmission::ExpectedBlocker {
            capability_ids: mapping.capability_ids.clone(),
            outcome: result.outcome.clone(),
        }
    };
    diagnostics.finish(admission)
}

#[derive(Debug)]
/// Ephemeral process output consumed immediately into normalized digests.
pub struct HarnessProcessOutput {
    status: Option<i32>,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

impl HarnessProcessOutput {
    /// Constructs transient output for a process adapter or deterministic test double.
    pub fn new(status: Option<i32>, stdout: Vec<u8>, stderr: Vec<u8>) -> Self {
        Self {
            status,
            stdout,
            stderr,
        }
    }
}

/// Process boundary used by the per-check runner; implementations execute no shell.
pub trait HarnessCommandExecutor {
    /// Digest of the exact CLI bytes this adapter will execute.
    fn cli_artifact_digest(&self) -> Result<Digest, ToolError>;

    /// Engine target selected by this process adapter.
    fn execution_target(&self) -> &TargetDigest;

    /// Digest of the exact Rust workspace or module artifact supplied to the harness.
    ///
    /// Implementations keep that artifact immutable for the whole execution. The runner treats
    /// this trait as the adapter boundary for workspace snapshotting.
    fn verified_artifact_digest(&self) -> &Digest;

    /// Executes the argv command with a cleared, allowlisted environment.
    fn execute(
        &self,
        command: &CommandSpec,
        repository_root: &Path,
    ) -> Result<HarnessProcessOutput, ToolError>;
}

#[derive(Clone, Debug)]
/// Exact-path operating-system process adapter for the Dagger CLI.
pub struct ProcessHarnessExecutor {
    cli_path: PathBuf,
    execution_target: TargetDigest,
    verified_artifact_digest: Digest,
}

impl ProcessHarnessExecutor {
    /// Selects exact CLI bytes, engine identity, and a precomputed immutable subject artifact.
    pub fn new(
        cli_path: PathBuf,
        execution_target: TargetDigest,
        verified_artifact_digest: Digest,
    ) -> Self {
        Self {
            cli_path,
            execution_target,
            verified_artifact_digest,
        }
    }

    fn canonical_cli_path(&self) -> Result<PathBuf, ToolError> {
        self.cli_path
            .canonicalize()
            .map_err(|error| ToolError::io("canonicalize selected Dagger CLI", &error, None))
    }
}

impl HarnessCommandExecutor for ProcessHarnessExecutor {
    fn cli_artifact_digest(&self) -> Result<Digest, ToolError> {
        let cli_path = self.canonical_cli_path()?;
        fs::read(cli_path)
            .map(Digest::sha256)
            .map_err(|error| ToolError::io("read selected Dagger CLI", &error, None))
    }

    fn execution_target(&self) -> &TargetDigest {
        &self.execution_target
    }

    fn verified_artifact_digest(&self) -> &Digest {
        &self.verified_artifact_digest
    }

    fn execute(
        &self,
        command: &CommandSpec,
        repository_root: &Path,
    ) -> Result<HarnessProcessOutput, ToolError> {
        let canonical_root = repository_root
            .canonicalize()
            .map_err(|error| ToolError::io("canonicalize harness repository root", &error, None))?;
        let working_directory = repository_root.join(command.working_directory.as_str());
        let canonical_working_directory = working_directory.canonicalize().map_err(|error| {
            ToolError::io(
                "canonicalize harness working directory",
                &error,
                Some(command.working_directory.clone()),
            )
        })?;
        if !canonical_working_directory.starts_with(&canonical_root) {
            let error = std::io::Error::new(
                std::io::ErrorKind::PermissionDenied,
                "working directory escaped repository root",
            );
            return Err(ToolError::io(
                "contain harness working directory",
                &error,
                Some(command.working_directory.clone()),
            ));
        }
        let cli_path = self.canonical_cli_path()?;
        let output = Command::new(cli_path)
            .args(&command.args)
            .current_dir(&canonical_working_directory)
            .env_clear()
            .envs(&command.environment)
            .output()
            .map_err(|error| {
                ToolError::io(
                    "execute Dagger harness check",
                    &error,
                    Some(command.working_directory.clone()),
                )
            })?;
        Ok(HarnessProcessOutput::new(
            output.status.code(),
            output.stdout,
            output.stderr,
        ))
    }
}

/// Immutable identities and command policy required for one per-check execution.
pub struct HarnessRunContext<'a> {
    pub harness_revision: &'a CommitSha,
    pub target: &'a TargetDigest,
    pub verified_artifact_digest: &'a Digest,
    pub platform: Platform,
    pub command_policy: &'a CommandPolicy,
}

/// Executes one already-validated public check and retains only normalized identities and digests.
pub fn run_harness_check(
    mapping: &HarnessCheckMapping,
    context: &HarnessRunContext<'_>,
    executor: &impl HarnessCommandExecutor,
    repository_root: &Path,
) -> Validation<Result<HarnessCheckResult, ToolError>> {
    let mut diagnostics = DiagnosticCollector::default();
    if mapping.harness_revision != *context.harness_revision {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessRevisionMismatch,
            mapping,
            "runner module revision differs from the immutable mapping revision",
        ));
    }
    if mapping.execution_target != *context.target
        || mapping.execution_target != *executor.execution_target()
        || mapping.verified_artifact_digest != *context.verified_artifact_digest
        || mapping.verified_artifact_digest != *executor.verified_artifact_digest()
    {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessTargetMismatch,
            mapping,
            "runner engine or verified Rust artifact differs from the mapping",
        ));
    }
    if !mapping.platform_scope.contains(&context.platform) {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessPlatformInvalid,
            mapping,
            "runner platform is outside the mapping scope",
        ));
    }
    if !is_public_check_invocation(&mapping.invocation, &mapping.check_id) {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessInvocationInvalid,
            mapping,
            "runner accepts only the pinned public per-check argv interface",
        ));
    }
    for detail in command_defects(&mapping.invocation, context.command_policy) {
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessInvocationInvalid,
            mapping,
            detail,
        ));
    }
    // Contract defects are resolved before any process adapter is consulted. Otherwise an I/O
    // failure could mask invalid durable input and make rejection depend on the host machine.
    diagnostics.finish(())?;

    let actual_cli_digest = match executor.cli_artifact_digest() {
        Ok(digest) => digest,
        Err(error) => return Ok(Err(error)),
    };
    if actual_cli_digest != mapping.cli_artifact_digest {
        let mut diagnostics = DiagnosticCollector::default();
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessTargetMismatch,
            mapping,
            "selected executable bytes differ from the mapped CLI artifact",
        ));
        diagnostics.finish(())?;
    }

    let output = match executor.execute(&mapping.invocation, repository_root) {
        Ok(output) => output,
        Err(error) => return Ok(Err(error)),
    };
    // Re-hash after execution so ordinary replacement or mutation of the selected path cannot
    // produce a durable result under the pre-execution CLI identity.
    let final_cli_digest = match executor.cli_artifact_digest() {
        Ok(digest) => digest,
        Err(error) => return Ok(Err(error)),
    };
    if final_cli_digest != mapping.cli_artifact_digest {
        let mut diagnostics = DiagnosticCollector::default();
        diagnostics.push(mapping_diagnostic(
            DiagnosticCode::HarnessTargetMismatch,
            mapping,
            "selected CLI bytes changed while the check was executing",
        ));
        diagnostics.finish(())?;
    }
    let outcome = if output.status == Some(0) {
        CheckOutcome::Passed
    } else {
        CheckOutcome::Failed
    };
    Ok(Ok(HarnessCheckResult {
        check_id: mapping.check_id.clone(),
        check_kind: mapping.check_kind.clone(),
        harness_revision: mapping.harness_revision.clone(),
        target: mapping.execution_target.clone(),
        cli_artifact_digest: mapping.cli_artifact_digest.clone(),
        verified_artifact_digest: mapping.verified_artifact_digest.clone(),
        platform: context.platform.clone(),
        outcome,
        assertion: mapping.expected_outcome.assertion.clone(),
        capability_ids: mapping.capability_ids.clone(),
        stdout_digest: Digest::sha256(output.stdout),
        stderr_digest: Digest::sha256(output.stderr),
    }))
}

/// Validates the portable Feature 8 extension boundary without importing Go command syntax.
pub fn validate_conformance_scenario(
    scenario: &ConformanceScenario,
    inventory: &CanonicalInventory,
    command_policy: &CommandPolicy,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    if scenario.capability_ids.is_empty()
        || scenario
            .capability_ids
            .iter()
            .any(|id| !inventory.capabilities.contains_key(id))
    {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessCapabilityMissing,
            scenario.scenario_id.to_string(),
            None,
            "extension requires a non-empty exact set of current Capability_IDs",
        ));
    }
    let anchored_scope = CanonicalSet::new(
        scenario
            .source_anchors
            .iter()
            .flat_map(|anchor| anchor.proved_capability_ids.iter().cloned()),
    );
    if scenario.source_anchors.is_empty()
        || scenario
            .source_anchors
            .iter()
            .any(|anchor| anchor.evidence_kind != EvidenceKind::Authority)
        || anchored_scope != scenario.capability_ids
    {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessScopeInvalid,
            scenario.scenario_id.to_string(),
            None,
            "source anchors must exactly ground the extension's capability scope",
        ));
    }
    if !observable_behavior_is_portable(&scenario.observable_behavior) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessScopeInvalid,
            scenario.scenario_id.to_string(),
            None,
            "extension must preserve normalized observable behaviour, not only a command port",
        ));
    }
    if invocation_is_go_specific_or_obsolete(&scenario.invocation) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessInvocationInvalid,
            scenario.scenario_id.to_string(),
            None,
            "extension invocation contains Go-specific or obsolete CLI syntax",
        ));
    }
    for detail in command_defects(&scenario.invocation, command_policy) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessInvocationInvalid,
            scenario.scenario_id.to_string(),
            None,
            detail,
        ));
    }
    if scenario.expected_outcome.outcome != CheckOutcome::Passed {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessOutcomeMissing,
            scenario.scenario_id.to_string(),
            None,
            "extension must define its passing behavioural assertion",
        ));
    }
    diagnostics.finish(())
}

fn observable_behavior_is_portable(value: &serde_json::Value) -> bool {
    if canonical_bytes(value).is_err() || value.is_null() {
        return false;
    }
    match value {
        serde_json::Value::Object(fields) => {
            !fields.is_empty()
                && fields.keys().any(|key| {
                    !matches!(
                        key.to_ascii_lowercase().as_str(),
                        "command" | "program" | "args" | "cli"
                    )
                })
        }
        serde_json::Value::Array(values) => !values.is_empty(),
        serde_json::Value::String(_) => false,
        serde_json::Value::Bool(_) | serde_json::Value::Number(_) => true,
        serde_json::Value::Null => false,
    }
}

fn invocation_is_go_specific_or_obsolete(command: &CommandSpec) -> bool {
    command.program.as_str() == "go"
        || command.args.iter().any(|arg| {
            let arg = arg.to_ascii_lowercase();
            arg == "do"
                || arg == "--mod"
                || arg.contains("--sdk=go")
                || arg.contains("--sdk go")
                || arg.ends_with(".go")
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn public_check_argv_is_exact_and_ordered() {
        let check_id = CheckId::new("module-start").unwrap();
        let command = CommandSpec {
            program: crate::model::ExecutableId::new("dagger").unwrap(),
            args: vec![
                "check".to_owned(),
                "module-start".to_owned(),
                "--no-generate".to_owned(),
            ],
            working_directory: crate::model::RepositoryRelativePath::new("sdk/rust").unwrap(),
            environment: BTreeMap::new(),
        };

        assert!(is_public_check_invocation(&command, &check_id));
    }

    #[test]
    fn command_only_observable_shape_is_not_a_portable_scenario() {
        assert!(!observable_behavior_is_portable(&serde_json::json!({
            "command": "dagger do something"
        })));
    }
}
