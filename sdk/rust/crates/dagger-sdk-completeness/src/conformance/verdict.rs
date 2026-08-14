//! Immutable exact-target run plans and one atomic Rust-owned sign-off verdict.
//!
//! Adapters may collect observations in any order, but they cannot decide which subset counts as
//! success. This module normalizes the complete observation tree, evaluates every gate together,
//! and hashes the resulting pass-or-fail record. A failed verdict deliberately carries no proved
//! capability subset, so retry or reporting code cannot accidentally promote partial evidence.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};
use thiserror::Error;

use crate::canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
use crate::model::{
    Architecture, CanonicalSet, CapabilityId, CommitSha, Digest, OperatingSystem, TargetDigest,
};

use super::{
    AdmittedStableConnector, ArtifactComponent, ArtifactCounters, ArtifactImportReceipt,
    ArtifactMaterialization, ArtifactPlan, CaseAttemptOutcome, CaseCatalog, CaseExecutionBinding,
    ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, FacadeRouteRegistry,
    InstalledRustBaseline, NetworkPolicyId, NonZeroCount, NonZeroMillis, PlatformDescriptor,
    SignoffCaseId, SignoffCaseObservation, SubjectIdentity, ToolchainRole, VerifiedArtifactBundle,
    required_artifact_components, required_artifact_toolchains, validate_case_observation,
};

const MAX_SIGNOFF_OBSERVATION_BYTES: usize = 64 * 1024 * 1024;

/// Closed network behaviour attached to one reviewed sign-off policy identity.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum SignoffNetworkPolicy {
    /// Only the already validated exact-target engine service is reachable.
    EngineOnly,
    /// Only immutable, digest-bound remote inputs are reachable.
    ImmutableRemote,
    /// Immutable distribution metadata and the exact-target engine are reachable.
    ManifestAndEngine,
    /// The exact engine plus the closed public dependencies of one committed Rust example.
    ReadOnlyPublicDependencies,
}

/// Complete immutable declaration admitted before exact-target orchestration begins.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SignoffRunPlan {
    /// Durable plan format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target under evaluation.
    pub target_digest: TargetDigest,
    /// Full reachable fork revision containing the Rust implementation.
    pub subject_revision: CommitSha,
    /// Initial exact-engine sign-off platform.
    pub platform: PlatformDescriptor,
    /// Admitted host resource and tool identity.
    pub host_profile_digest: Digest,
    /// Earlier host preflight identity, including its isolated smoke record.
    pub preflight_digest: Digest,
    /// Exclusive Build or Import declaration for the one reusable artifact.
    pub artifact_plan: ArtifactPlan,
    /// Consume-only child implementation closure.
    pub closure_bundle_digest: Digest,
    /// Exact closed case catalog.
    pub case_catalog_digest: Digest,
    /// Closed network policy definitions used by every catalog case.
    pub network_policies: BTreeMap<NetworkPolicyId, SignoffNetworkPolicy>,
    /// Positive upper bound for isolated case fan-out.
    pub maximum_concurrency: NonZeroCount,
    /// Exact number of reviewed Rust invocations after authority aliases are grouped.
    pub expected_case_executions: NonZeroCount,
    /// Positive aggregate wall-clock budget for the whole sign-off invocation.
    pub total_budget: NonZeroMillis,
}

/// Constructs the fixed authoritative Import plan from independently admitted identities.
pub fn assemble_signoff_run_plan(
    artifact_plan: ArtifactPlan,
    closure_bundle_digest: Digest,
    catalog: &CaseCatalog,
    routes: &FacadeRouteRegistry,
    host_profile_digest: Digest,
    preflight_digest: Digest,
) -> Result<SignoffRunPlan, ConformanceDiagnosticSet> {
    if !matches!(
        artifact_plan.materialization,
        ArtifactMaterialization::Import { .. }
    ) {
        return Err(ConformanceDiagnosticSet::new([verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffArtifactStateInvalid,
            DiagnosticPhase::Artifact,
            "authoritative sign-off requires the retained Import artifact strategy",
        )])
        .expect("one diagnostic is present"));
    }
    let network_policies = catalog
        .cases()
        .values()
        .map(|case| {
            policy_for_id(&case.network)
                .map(|policy| (case.network.clone(), policy))
                .ok_or_else(|| {
                    ConformanceDiagnosticSet::new([verdict_diagnostic(
                        ConformanceDiagnosticCode::SignoffUnrelatedWork,
                        DiagnosticPhase::Case,
                        "case catalog names an unknown sign-off network policy",
                    )])
                    .expect("one diagnostic is present")
                })
        })
        .collect::<Result<BTreeMap<_, _>, _>>()?;
    let plan = SignoffRunPlan {
        format_version: ConformanceFormatVersion::V1,
        target_digest: catalog.target_digest().clone(),
        subject_revision: artifact_plan.subject.revision.clone(),
        platform: PlatformDescriptor::linux_amd64(),
        host_profile_digest,
        preflight_digest,
        artifact_plan,
        closure_bundle_digest,
        case_catalog_digest: catalog.digest().clone(),
        network_policies,
        maximum_concurrency: NonZeroCount::new(8).expect("fixed concurrency is bounded"),
        expected_case_executions: NonZeroCount::new(routes.physical_executions())
            .expect("admitted route registry is non-empty and bounded"),
        total_budget: NonZeroMillis::new(6 * 60 * 60 * 1_000)
            .expect("fixed six-hour sign-off budget is bounded"),
    };
    signoff_run_plan_digest(&plan, catalog)?;
    Ok(plan)
}

/// Complete counted work for one sign-off invocation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SignoffExecutionCounts {
    /// Engine start already contained in the earlier host preflight record.
    pub preflight_smoke_engine_starts: u32,
    /// Orchestration-engine invocations used by this sign-off run.
    pub orchestration_engine_starts: u32,
    /// Exact artifact construction/import/component counters.
    pub artifact: ArtifactCounters,
    /// Exact-target engine service starts.
    pub exact_target_engine_starts: u32,
    /// Exact-target engine service stops.
    pub exact_target_engine_stops: u32,
    /// Exact-target child-process reap observations.
    pub exact_target_child_reaps: u32,
    /// Installed Rust baseline materializations.
    pub rust_baseline_materializations: u32,
    /// Reviewed Rust case invocations actually executed before verdict-row expansion.
    pub case_executions: u32,
    /// Child implementation evidence replays; this must remain zero.
    pub closure_replays: u32,
    /// Unrelated SDK, generation, distribution, or target actions.
    pub unrelated_actions: u32,
}

/// Positive timing for every shared exact-target sign-off phase.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SignoffPhaseTimings {
    /// Artifact build or verified import duration.
    pub artifact: NonZeroMillis,
    /// Validated exact-target engine startup duration.
    pub engine_startup: NonZeroMillis,
    /// One packaged Rust SDK installation duration.
    pub rust_installation: NonZeroMillis,
    /// Exact retained payload security-scan duration.
    pub security_scan: NonZeroMillis,
    /// Sum of all retained case-attempt durations.
    pub case_execution: NonZeroMillis,
    /// Exact-target engine stop, reap, and retained-output cleanup duration.
    pub cleanup: NonZeroMillis,
    /// Exact sum of the six preceding phases.
    pub total: NonZeroMillis,
}

/// Forbidden observation which remains representable so verdict derivation can fail closed.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ForbiddenSignoffEvent {
    /// A non-Rust SDK builder or test suite ran.
    UnrelatedSdk,
    /// A repository distribution graph ran.
    Distribution,
    /// Unscoped generation ran.
    UnscopedGeneration,
    /// A second exact-target engine was started.
    DuplicateTargetEngine,
    /// A second installed Rust baseline was created.
    DuplicateRustBaseline,
    /// Child implementation evidence was replayed.
    ClosureReplay,
    /// A secret canary appeared in an inspected output domain.
    SecretCanaryLeak,
}

/// Raw, bounded observation projection supplied by host and Dagger adapters.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SignoffObservation {
    /// Run-plan identity independently retained by the adapter.
    pub run_plan_digest: Digest,
    /// Host profile identity actually used by the invocation.
    pub host_profile_digest: Digest,
    /// Earlier preflight record actually consumed by the invocation.
    pub host_preflight_digest: Digest,
    /// Canonical exact-target artifact manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Direct identity of the retained OCI payload bytes.
    pub artifact_payload_digest: Digest,
    /// Canonical receipt emitted by the sole authoritative artifact Import graph.
    pub artifact_import_receipt: ArtifactImportReceipt,
    /// Consume-only implementation closure identity.
    pub closure_bundle_digest: Digest,
    /// Supported Linux/macOS native-platform evidence identity.
    pub platform_matrix_digest: Digest,
    /// Complete exact-artifact security result identity.
    pub security_report_digest: Digest,
    /// Independently observed exact engine identity.
    pub engine_identity_digest: Digest,
    /// One installed packaged Rust baseline.
    pub baseline: InstalledRustBaseline,
    /// Stable connector identity and claim admitted against that exact baseline.
    pub stable_connector: AdmittedStableConnector,
    /// Every shared build, import, engine, baseline, and unrelated-work counter.
    pub execution_counts: SignoffExecutionCounts,
    /// Every shared phase duration, including exact total accounting.
    pub phase_timings: SignoffPhaseTimings,
    /// Complete case observations; declaration order is not semantic.
    pub cases: Vec<SignoffCaseObservation>,
    /// Capability set claimed by the complete observation.
    pub claimed_capability_ids: CanonicalSet<CapabilityId>,
    /// Whether the admitted portable matrix and initial exact-engine platform gate passed.
    pub platform_gate_passed: bool,
    /// Whether dependency, policy, artifact scan, provenance, and exception gates passed.
    pub security_gate_passed: bool,
    /// Number of secret canary leak observations.
    pub secret_canary_leaks: u32,
    /// Forbidden graph events retained before normalization; duplicates remain observable.
    pub forbidden_events: Vec<ForbiddenSignoffEvent>,
}

/// Immutable inputs already admitted by their owning policy layers.
pub struct SignoffAdmissionContext<'a> {
    /// Complete run declaration.
    pub run_plan: &'a SignoffRunPlan,
    /// Closed case catalog and exact capability reverse index.
    pub case_catalog: &'a CaseCatalog,
    /// Immutable case bindings produced from the admitted artifact, engine, and baseline.
    pub case_bindings: &'a BTreeMap<SignoffCaseId, CaseExecutionBinding>,
    /// Exact artifact manifest admitted by the artifact state machine.
    pub artifact_manifest_digest: &'a Digest,
    /// Exact existing OCI payload admitted by the artifact state machine.
    pub artifact_payload_digest: &'a Digest,
    /// Canonical Import receipt independently re-admitted against the exact artifact.
    pub artifact_import_receipt: &'a ArtifactImportReceipt,
    /// Supported Linux/macOS evidence admitted independently of exact-engine sign-off.
    pub platform_matrix_digest: &'a Digest,
    /// Exact-artifact security report admitted by security policy.
    pub security_report_digest: &'a Digest,
    /// Validated exact engine identity.
    pub engine_identity_digest: &'a Digest,
    /// Sole installed packaged Rust baseline identity.
    pub baseline_digest: &'a Digest,
    /// Stable connector evidence independently admitted against the same baseline.
    pub stable_connector: &'a AdmittedStableConnector,
}

/// Atomic decision: failure never retains a promotable capability subset.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "decision", deny_unknown_fields)]
pub enum VerdictDecision {
    /// Every required relation and gate passed together.
    Passed {
        /// Exact capabilities proved by the complete passed case set.
        capability_ids: CanonicalSet<CapabilityId>,
    },
    /// At least one required relation or gate failed.
    Failed {
        /// Stable normalized reasons; no raw adapter output is retained.
        diagnostics: ConformanceDiagnosticSet,
    },
}

/// One canonical pass-or-fail record covering the complete exact-target invocation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AtomicSignoffVerdict {
    /// Durable verdict format.
    pub format_version: ConformanceFormatVersion,
    /// Domain-separated identity of every other field in this value.
    pub verdict_digest: Digest,
    /// Exact Dagger target.
    pub target_digest: TargetDigest,
    /// Full fork revision containing the evaluated Rust source.
    pub subject_revision: CommitSha,
    /// Exact-engine platform; portable platform closure remains a separate digest.
    pub platform: PlatformDescriptor,
    /// Admitted host profile.
    pub host_profile_digest: Digest,
    /// Complete immutable run declaration.
    pub run_plan_digest: Digest,
    /// Earlier isolated preflight record.
    pub host_preflight_digest: Digest,
    /// Exact artifact manifest.
    pub artifact_manifest_digest: Digest,
    /// Exact retained OCI payload.
    pub artifact_payload_digest: Digest,
    /// Canonical receipt proving the sole authoritative artifact Import graph.
    pub artifact_import_receipt: ArtifactImportReceipt,
    /// Consume-only implementation closure.
    pub closure_bundle_digest: Digest,
    /// Complete closed case catalog.
    pub case_catalog_digest: Digest,
    /// Portable platform closure.
    pub platform_matrix_digest: Digest,
    /// Exact-artifact security closure.
    pub security_report_digest: Digest,
    /// Validated exact engine identity.
    pub engine_identity_digest: Digest,
    /// Sole installed packaged Rust baseline identity.
    pub baseline_digest: Digest,
    /// Exact stable-connector evidence retained from the production SDK instance.
    pub stable_connector: AdmittedStableConnector,
    /// Every shared work counter.
    pub execution_counts: SignoffExecutionCounts,
    /// Every required shared timing.
    pub phase_timings: SignoffPhaseTimings,
    /// Every case and retained attempt in stable case-ID order.
    pub cases: BTreeMap<SignoffCaseId, SignoffCaseObservation>,
    /// Raw claimed capability set, retained even when overbroad so mutation changes identity.
    pub claimed_capability_ids: CanonicalSet<CapabilityId>,
    /// Observed portable/exact platform gate result.
    pub platform_gate_passed: bool,
    /// Observed security gate result.
    pub security_gate_passed: bool,
    /// Exact canary leak count.
    pub secret_canary_leaks: u32,
    /// Canonical forbidden-event set.
    pub forbidden_events: CanonicalSet<ForbiddenSignoffEvent>,
    /// One indivisible pass-or-fail decision.
    pub decision: VerdictDecision,
}

/// Deliberately narrow authority carried by a retained release handoff.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ReleaseHandoffAuthority {
    /// The record proves retained sign-off evidence but cannot publish or widen support.
    EvidenceOnly,
}

/// Exact retained bytes and one-platform verdict made available to release policy.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReleaseHandoffRecord {
    /// Durable handoff format.
    pub format_version: ConformanceFormatVersion,
    /// Domain-separated identity of every other field in this record.
    pub handoff_digest: Digest,
    /// Exact Dagger target whose bytes passed sign-off.
    pub target_digest: TargetDigest,
    /// Full reachable fork revision contained in the retained payload.
    pub subject_revision: CommitSha,
    /// Sole exact-engine platform proved by this record.
    pub platform: PlatformDescriptor,
    /// Direct identity of the complete retained outer bundle bytes.
    pub signoff_bundle_digest: Digest,
    /// Canonical exact-target manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Direct identity of the retained inner OCI payload bytes.
    pub artifact_payload_digest: Digest,
    /// Canonical Import-receipt identity bound by the passing verdict.
    pub artifact_import_receipt_digest: Digest,
    /// Security result for those exact manifest and payload bytes.
    pub security_report_digest: Digest,
    /// Exact stable-connector evidence retained by the passing verdict.
    pub stable_connector: AdmittedStableConnector,
    /// Passing imported-artifact verdict covering this record.
    pub verdict_digest: Digest,
    /// Evidence-only authority; downstream release policy owns every publication decision.
    pub authority: ReleaseHandoffAuthority,
}

#[derive(Serialize)]
struct ReleaseHandoffDigestProjection<'a> {
    format_version: ConformanceFormatVersion,
    target_digest: &'a TargetDigest,
    subject_revision: &'a CommitSha,
    platform: &'a PlatformDescriptor,
    signoff_bundle_digest: &'a Digest,
    artifact_manifest_digest: &'a Digest,
    artifact_payload_digest: &'a Digest,
    artifact_import_receipt_digest: &'a Digest,
    security_report_digest: &'a Digest,
    stable_connector: &'a AdmittedStableConnector,
    verdict_digest: &'a Digest,
    authority: ReleaseHandoffAuthority,
}

impl ReleaseHandoffRecord {
    fn projection(&self) -> ReleaseHandoffDigestProjection<'_> {
        ReleaseHandoffDigestProjection {
            format_version: self.format_version,
            target_digest: &self.target_digest,
            subject_revision: &self.subject_revision,
            platform: &self.platform,
            signoff_bundle_digest: &self.signoff_bundle_digest,
            artifact_manifest_digest: &self.artifact_manifest_digest,
            artifact_payload_digest: &self.artifact_payload_digest,
            artifact_import_receipt_digest: &self.artifact_import_receipt_digest,
            security_report_digest: &self.security_report_digest,
            stable_connector: &self.stable_connector,
            verdict_digest: &self.verdict_digest,
            authority: self.authority,
        }
    }
}

#[derive(Serialize)]
struct VerdictDigestProjection<'a> {
    format_version: ConformanceFormatVersion,
    target_digest: &'a TargetDigest,
    subject_revision: &'a CommitSha,
    platform: &'a PlatformDescriptor,
    host_profile_digest: &'a Digest,
    run_plan_digest: &'a Digest,
    host_preflight_digest: &'a Digest,
    artifact_manifest_digest: &'a Digest,
    artifact_payload_digest: &'a Digest,
    artifact_import_receipt: &'a ArtifactImportReceipt,
    closure_bundle_digest: &'a Digest,
    case_catalog_digest: &'a Digest,
    platform_matrix_digest: &'a Digest,
    security_report_digest: &'a Digest,
    engine_identity_digest: &'a Digest,
    baseline_digest: &'a Digest,
    stable_connector: &'a AdmittedStableConnector,
    execution_counts: &'a SignoffExecutionCounts,
    phase_timings: &'a SignoffPhaseTimings,
    cases: &'a BTreeMap<SignoffCaseId, SignoffCaseObservation>,
    claimed_capability_ids: &'a CanonicalSet<CapabilityId>,
    platform_gate_passed: bool,
    security_gate_passed: bool,
    secret_canary_leaks: u32,
    forbidden_events: &'a CanonicalSet<ForbiddenSignoffEvent>,
    decision: &'a VerdictDecision,
}

impl AtomicSignoffVerdict {
    fn projection(&self) -> VerdictDigestProjection<'_> {
        VerdictDigestProjection {
            format_version: self.format_version,
            target_digest: &self.target_digest,
            subject_revision: &self.subject_revision,
            platform: &self.platform,
            host_profile_digest: &self.host_profile_digest,
            run_plan_digest: &self.run_plan_digest,
            host_preflight_digest: &self.host_preflight_digest,
            artifact_manifest_digest: &self.artifact_manifest_digest,
            artifact_payload_digest: &self.artifact_payload_digest,
            artifact_import_receipt: &self.artifact_import_receipt,
            closure_bundle_digest: &self.closure_bundle_digest,
            case_catalog_digest: &self.case_catalog_digest,
            platform_matrix_digest: &self.platform_matrix_digest,
            security_report_digest: &self.security_report_digest,
            engine_identity_digest: &self.engine_identity_digest,
            baseline_digest: &self.baseline_digest,
            stable_connector: &self.stable_connector,
            execution_counts: &self.execution_counts,
            phase_timings: &self.phase_timings,
            cases: &self.cases,
            claimed_capability_ids: &self.claimed_capability_ids,
            platform_gate_passed: self.platform_gate_passed,
            security_gate_passed: self.security_gate_passed,
            secret_canary_leaks: self.secret_canary_leaks,
            forbidden_events: &self.forbidden_events,
            decision: &self.decision,
        }
    }
}

/// Failure to decode bounded canonical sign-off input or verify a persisted verdict identity.
#[derive(Debug, Error)]
pub enum SignoffDecodeError {
    /// Input exceeded the durable observation size bound.
    #[error("sign-off input exceeds the durable size bound")]
    ExcessInput,
    /// Input was not canonical, typed JSON.
    #[error(transparent)]
    Canonical(#[from] CanonicalError),
    /// A verdict's embedded digest did not cover its complete decoded contents.
    #[error("atomic sign-off verdict digest mismatch")]
    VerdictDigestMismatch,
    /// A release handoff's embedded digest did not cover its complete decoded contents.
    #[error("release handoff digest mismatch")]
    ReleaseHandoffDigestMismatch,
}

/// Validates a run plan against the closed catalog and returns its canonical identity.
pub fn signoff_run_plan_digest(
    plan: &SignoffRunPlan,
    catalog: &CaseCatalog,
) -> Result<Digest, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    let initial_platform = plan.platform == PlatformDescriptor::linux_amd64()
        && plan.platform.operating_system == OperatingSystem::Linux
        && plan.platform.architecture == Architecture::Amd64;
    if plan.format_version != ConformanceFormatVersion::V1
        || !initial_platform
        || plan.target_digest != *catalog.target_digest()
        || plan.artifact_plan.target_descriptor_digest != plan.target_digest
        || plan.artifact_plan.platform != plan.platform
        || plan.case_catalog_digest != *catalog.digest()
        || plan.platform != *catalog.platform()
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffVerdictIncomplete,
            DiagnosticPhase::Verdict,
            "run plan target platform or catalog identity is inconsistent",
        ));
    }
    let subject = &plan.artifact_plan.subject;
    let subject_matches_catalog = match catalog.subject() {
        SubjectIdentity::Revision(revision) => revision == &plan.subject_revision,
        SubjectIdentity::SourceDigest(digest) => digest == &subject.focused_source_digest,
    };
    if plan.subject_revision != subject.revision
        || !subject_matches_catalog
        || !subject.reachable
        || !subject.clean
        || !subject.immutable
        || subject.focused_source_digest != subject.workspace_focused_source_digest
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffVerdictIncomplete,
            DiagnosticPhase::Artifact,
            "subject revision is dirty stale mutable or unreachable",
        ));
    }
    let components = plan
        .artifact_plan
        .components
        .keys()
        .copied()
        .collect::<BTreeSet<_>>();
    let required_components = required_artifact_components()
        .into_iter()
        .collect::<BTreeSet<ArtifactComponent>>();
    let toolchains = plan
        .artifact_plan
        .toolchain_digests
        .keys()
        .copied()
        .collect::<BTreeSet<_>>();
    let required_toolchains = required_artifact_toolchains()
        .into_iter()
        .collect::<BTreeSet<ToolchainRole>>();
    if components != required_components || toolchains != required_toolchains {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffArtifactManifestInvalid,
            DiagnosticPhase::Artifact,
            "artifact plan omits a required component or toolchain identity",
        ));
    }
    let expected_policies = catalog
        .cases()
        .values()
        .map(|case| case.network.clone())
        .collect::<BTreeSet<_>>();
    if plan.network_policies.len() != expected_policies.len()
        || expected_policies
            .iter()
            .any(|id| plan.network_policies.get(id).copied() != policy_for_id(id))
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffUnrelatedWork,
            DiagnosticPhase::Case,
            "run plan contains an unknown or incomplete network policy",
        ));
    }
    if let Some(diagnostics) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    canonical_digest(DigestDomain::ConformanceRunPlan, plan).map_err(|_| {
        ConformanceDiagnosticSet::new([verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffVerdictIncomplete,
            DiagnosticPhase::Verdict,
            "run plan could not be encoded canonically",
        )])
        .expect("one diagnostic is present")
    })
}

/// Decodes one canonical raw observation after applying the gross byte-size bound.
pub fn decode_signoff_observation(bytes: &[u8]) -> Result<SignoffObservation, SignoffDecodeError> {
    if bytes.len() > MAX_SIGNOFF_OBSERVATION_BYTES {
        return Err(SignoffDecodeError::ExcessInput);
    }
    Ok(decode_canonical(bytes)?)
}

/// Derives one total pass-or-fail verdict from every admitted input and raw observation.
pub fn derive_atomic_signoff_verdict(
    context: &SignoffAdmissionContext<'_>,
    observation: SignoffObservation,
) -> AtomicSignoffVerdict {
    let mut diagnostics = Vec::new();
    let expected_run_plan_digest =
        match signoff_run_plan_digest(context.run_plan, context.case_catalog) {
            Ok(digest) => digest,
            Err(errors) => {
                diagnostics.extend(errors.into_inner());
                canonical_digest(DigestDomain::ConformanceRunPlan, context.run_plan)
                    .expect("typed run plans always encode")
            }
        };
    if observation.run_plan_digest != expected_run_plan_digest
        || observation.host_profile_digest != context.run_plan.host_profile_digest
        || observation.host_preflight_digest != context.run_plan.preflight_digest
        || observation.closure_bundle_digest != context.run_plan.closure_bundle_digest
        || observation.artifact_manifest_digest != *context.artifact_manifest_digest
        || observation.artifact_payload_digest != *context.artifact_payload_digest
        || observation.artifact_import_receipt != *context.artifact_import_receipt
        || observation.platform_matrix_digest != *context.platform_matrix_digest
        || observation.security_report_digest != *context.security_report_digest
        || observation.engine_identity_digest != *context.engine_identity_digest
        || observation.baseline.baseline_digest != *context.baseline_digest
        || observation.baseline.artifact_manifest_digest != *context.artifact_manifest_digest
        || observation.baseline.artifact_payload_digest != *context.artifact_payload_digest
        || observation.baseline.engine.identity_digest != *context.engine_identity_digest
        || observation.stable_connector != *context.stable_connector
        || observation.stable_connector.baseline_digest != *context.baseline_digest
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffVerdictIncomplete,
            DiagnosticPhase::Verdict,
            "one or more admitted sign-off identities are stale or mismatched",
        ));
    }
    if let ArtifactMaterialization::Import {
        manifest_digest,
        payload_digest,
    } = &context.run_plan.artifact_plan.materialization
        && (manifest_digest != context.artifact_manifest_digest
            || payload_digest != context.artifact_payload_digest)
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffArtifactManifestInvalid,
            DiagnosticPhase::Artifact,
            "import plan does not name the admitted artifact bytes",
        ));
    }
    validate_execution_counts(
        context.run_plan,
        &observation.execution_counts,
        &mut diagnostics,
    );

    let forbidden_events = CanonicalSet::new(observation.forbidden_events.iter().copied());
    if forbidden_events.len() != observation.forbidden_events.len()
        || !forbidden_events.is_empty()
        || observation.execution_counts.unrelated_actions != 0
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffUnrelatedWork,
            DiagnosticPhase::Verdict,
            "forbidden duplicate or unrelated sign-off work was observed",
        ));
    }
    if !observation.platform_gate_passed {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::PlatformClaimInvalid,
            DiagnosticPhase::Platform,
            "portable or exact-engine platform gate did not pass",
        ));
    }
    if !observation.security_gate_passed {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::ArtifactVulnerabilityGateFailed,
            DiagnosticPhase::Security,
            "artifact security gate did not pass",
        ));
    }
    if observation.secret_canary_leaks != 0
        || forbidden_events.contains(&ForbiddenSignoffEvent::SecretCanaryLeak)
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SecretCanaryLeak,
            DiagnosticPhase::Security,
            "secret canary leakage was observed",
        ));
    }

    let mut cases = BTreeMap::new();
    for case in observation.cases.iter().cloned() {
        if cases.insert(case.case_id.clone(), case).is_some() {
            diagnostics.push(verdict_diagnostic(
                ConformanceDiagnosticCode::SignoffCaseUnknown,
                DiagnosticPhase::Case,
                "duplicate case observation was supplied",
            ));
        }
    }
    let expected_case_ids = context
        .case_catalog
        .cases()
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>();
    let observed_case_ids = cases.keys().cloned().collect::<BTreeSet<_>>();
    if observed_case_ids != expected_case_ids {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffCaseSkipped,
            DiagnosticPhase::Case,
            "required case set is missing or contains an unknown case",
        ));
    }
    if context
        .case_bindings
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>()
        != expected_case_ids
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffCaseUnknown,
            DiagnosticPhase::Case,
            "case binding set does not equal the closed catalog",
        ));
    }

    for (case_id, case_observation) in &cases {
        let Some(case) = context.case_catalog.cases().get(case_id) else {
            continue;
        };
        let Some(binding) = context.case_bindings.get(case_id) else {
            continue;
        };
        if binding.catalog_digest != *context.case_catalog.digest()
            || binding.artifact_manifest_digest != *context.artifact_manifest_digest
            || binding.artifact_payload_digest != *context.artifact_payload_digest
            || binding.engine_identity_digest != *context.engine_identity_digest
            || binding.baseline_digest != *context.baseline_digest
        {
            diagnostics.push(case_diagnostic(
                ConformanceDiagnosticCode::SignoffCaseIsolationViolation,
                case_id,
                "case binding does not name the shared admitted baseline",
            ));
            continue;
        }
        if let Err(errors) = validate_case_observation(case, binding, case_observation) {
            diagnostics.extend(errors.into_inner());
            continue;
        }
        match &case_observation.final_outcome {
            CaseAttemptOutcome::Passed { .. } => {}
            CaseAttemptOutcome::AssertionFailed { diagnostic }
            | CaseAttemptOutcome::InfrastructureFailed { diagnostic, .. } => {
                diagnostics.push(diagnostic.clone());
            }
        }
    }

    let expected_capability_ids =
        CanonicalSet::new(context.case_catalog.capability_cases().keys().cloned());
    if observation.claimed_capability_ids != expected_capability_ids {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffVerdictIncomplete,
            DiagnosticPhase::Verdict,
            "claimed capability set differs from the complete proved case set",
        ));
    }
    validate_phase_timings(context.run_plan, &observation, &mut diagnostics);

    let decision = match ConformanceDiagnosticSet::new(diagnostics) {
        Some(diagnostics) => VerdictDecision::Failed { diagnostics },
        None => VerdictDecision::Passed {
            capability_ids: expected_capability_ids,
        },
    };
    let mut verdict = AtomicSignoffVerdict {
        format_version: ConformanceFormatVersion::V1,
        verdict_digest: Digest::sha256([]),
        target_digest: context.run_plan.target_digest.clone(),
        subject_revision: context.run_plan.subject_revision.clone(),
        platform: context.run_plan.platform.clone(),
        host_profile_digest: observation.host_profile_digest,
        run_plan_digest: observation.run_plan_digest,
        host_preflight_digest: observation.host_preflight_digest,
        artifact_manifest_digest: observation.artifact_manifest_digest,
        artifact_payload_digest: observation.artifact_payload_digest,
        artifact_import_receipt: observation.artifact_import_receipt,
        closure_bundle_digest: observation.closure_bundle_digest,
        case_catalog_digest: context.run_plan.case_catalog_digest.clone(),
        platform_matrix_digest: observation.platform_matrix_digest,
        security_report_digest: observation.security_report_digest,
        engine_identity_digest: observation.engine_identity_digest,
        baseline_digest: observation.baseline.baseline_digest,
        stable_connector: observation.stable_connector,
        execution_counts: observation.execution_counts,
        phase_timings: observation.phase_timings,
        cases,
        claimed_capability_ids: observation.claimed_capability_ids,
        platform_gate_passed: observation.platform_gate_passed,
        security_gate_passed: observation.security_gate_passed,
        secret_canary_leaks: observation.secret_canary_leaks,
        forbidden_events,
        decision,
    };
    verdict.verdict_digest =
        canonical_digest(DigestDomain::ConformanceVerdict, &verdict.projection())
            .expect("typed atomic verdicts always encode");
    verdict
}

/// Encodes one verdict as canonical JSON with its already verified embedded identity.
pub fn encode_atomic_signoff_verdict(
    verdict: &AtomicSignoffVerdict,
) -> Result<Vec<u8>, SignoffDecodeError> {
    verify_verdict_digest(verdict)?;
    Ok(canonical_bytes(verdict)?)
}

/// Decodes canonical JSON and independently rechecks the embedded verdict identity.
pub fn decode_atomic_signoff_verdict(
    bytes: &[u8],
) -> Result<AtomicSignoffVerdict, SignoffDecodeError> {
    if bytes.len() > MAX_SIGNOFF_OBSERVATION_BYTES {
        return Err(SignoffDecodeError::ExcessInput);
    }
    let verdict: AtomicSignoffVerdict = decode_canonical(bytes)?;
    verify_verdict_digest(&verdict)?;
    Ok(verdict)
}

/// Derives a release handoff only from retained bytes and a passing imported-artifact verdict.
///
/// The bundle parameter is intentionally a verified byte-owning value rather than a digest. This
/// prevents callers from manufacturing a release input after the signed-off payload has been
/// discarded. The returned authority is evidence-only and never permits publication or another
/// platform claim.
pub fn derive_release_handoff(
    bundle: &VerifiedArtifactBundle,
    verdict: &AtomicSignoffVerdict,
) -> Result<ReleaseHandoffRecord, ConformanceDiagnosticSet> {
    let imported_without_build = verdict.execution_counts.artifact.imports == 1
        && verdict.execution_counts.artifact.construction == 0
        && verdict
            .execution_counts
            .artifact
            .component_builds
            .values()
            .all(|count| *count == 0);
    let manifest = bundle.manifest();
    let exact_identity = !bundle.bytes().is_empty()
        && !bundle.payload().is_empty()
        && bundle.manifest_digest() == &verdict.artifact_manifest_digest
        && manifest.payload_digest == verdict.artifact_payload_digest
        && Digest::sha256(bundle.payload()) == verdict.artifact_payload_digest
        && manifest.target_descriptor_digest == verdict.target_digest
        && manifest.subject_revision == verdict.subject_revision
        && manifest.platform == verdict.platform;
    if verify_verdict_digest(verdict).is_err()
        || !matches!(verdict.decision, VerdictDecision::Passed { .. })
        || !imported_without_build
        || !exact_identity
    {
        return Err(release_handoff_error());
    }

    let mut record = ReleaseHandoffRecord {
        format_version: ConformanceFormatVersion::V1,
        handoff_digest: Digest::sha256([]),
        target_digest: verdict.target_digest.clone(),
        subject_revision: verdict.subject_revision.clone(),
        platform: verdict.platform.clone(),
        signoff_bundle_digest: bundle.bundle_digest().clone(),
        artifact_manifest_digest: verdict.artifact_manifest_digest.clone(),
        artifact_payload_digest: verdict.artifact_payload_digest.clone(),
        artifact_import_receipt_digest: verdict.artifact_import_receipt.receipt_digest.clone(),
        security_report_digest: verdict.security_report_digest.clone(),
        stable_connector: verdict.stable_connector.clone(),
        verdict_digest: verdict.verdict_digest.clone(),
        authority: ReleaseHandoffAuthority::EvidenceOnly,
    };
    record.handoff_digest = canonical_digest(
        DigestDomain::ConformanceReleaseHandoff,
        &record.projection(),
    )
    .map_err(|_| release_handoff_error())?;
    Ok(record)
}

/// Encodes one already verified release handoff as canonical JSON.
pub fn encode_release_handoff(
    handoff: &ReleaseHandoffRecord,
) -> Result<Vec<u8>, SignoffDecodeError> {
    verify_release_handoff_digest(handoff)?;
    Ok(canonical_bytes(handoff)?)
}

/// Decodes canonical release handoff JSON and independently rechecks its embedded identity.
pub fn decode_release_handoff(bytes: &[u8]) -> Result<ReleaseHandoffRecord, SignoffDecodeError> {
    if bytes.len() > MAX_SIGNOFF_OBSERVATION_BYTES {
        return Err(SignoffDecodeError::ExcessInput);
    }
    let handoff: ReleaseHandoffRecord = decode_canonical(bytes)?;
    verify_release_handoff_digest(&handoff)?;
    Ok(handoff)
}

/// Renders the handoff without implying publication or portable exact-engine support.
pub fn render_release_handoff(handoff: &ReleaseHandoffRecord) -> String {
    format!(
        "# Rust SDK release evidence handoff\n\nAuthority: `evidence-only`\n\nHandoff: `{}`\n\nVerdict: `{}`\n\nBundle: `{}`\n\nManifest: `{}`\n\nPayload: `{}`\n\nImport receipt: `{}`\n\nSecurity report: `{}`\n\nStable connector: `{}`\n\nSubject: `{}`\n\nPlatform: `{:?}/{:?}`\n\nThis record does not authorize publication or support for another platform.\n",
        handoff.handoff_digest,
        handoff.verdict_digest,
        handoff.signoff_bundle_digest,
        handoff.artifact_manifest_digest,
        handoff.artifact_payload_digest,
        handoff.artifact_import_receipt_digest,
        handoff.security_report_digest,
        handoff.stable_connector.observation_digest,
        handoff.subject_revision,
        handoff.platform.operating_system,
        handoff.platform.architecture,
    )
}

/// Renders the same atomic record as neutral Markdown without raw operational output.
pub fn render_atomic_signoff_verdict(verdict: &AtomicSignoffVerdict) -> String {
    let decision = match verdict.decision {
        VerdictDecision::Passed { .. } => "passed",
        VerdictDecision::Failed { .. } => "failed",
    };
    let mut rendered = format!(
        "# Rust SDK exact-target sign-off verdict\n\nDecision: `{decision}`\n\nVerdict: `{}`\n\nTarget: `{}`\n\nSubject: `{}`\n\nPlatform: `{:?}/{:?}`\n\n## Counted work\n\n| Work | Count |\n| --- | ---: |\n| Preflight smoke engine starts | {} |\n| Orchestration engine starts | {} |\n| Artifact constructions | {} |\n| Artifact imports | {} |\n| Exact-target engine starts | {} |\n| Rust baseline materializations | {} |\n| Reviewed Rust case executions | {} |\n| Closure replays | {} |\n| Unrelated actions | {} |\n\n## Phase timings\n\n| Phase | Milliseconds |\n| --- | ---: |\n| Artifact | {} |\n| Engine startup | {} |\n| Rust installation | {} |\n| Security scan | {} |\n| Case execution | {} |\n| Cleanup | {} |\n| Total | {} |\n\nCases: {}\n",
        verdict.verdict_digest,
        verdict.target_digest,
        verdict.subject_revision,
        verdict.platform.operating_system,
        verdict.platform.architecture,
        verdict.execution_counts.preflight_smoke_engine_starts,
        verdict.execution_counts.orchestration_engine_starts,
        verdict.execution_counts.artifact.construction,
        verdict.execution_counts.artifact.imports,
        verdict.execution_counts.exact_target_engine_starts,
        verdict.execution_counts.rust_baseline_materializations,
        verdict.execution_counts.case_executions,
        verdict.execution_counts.closure_replays,
        verdict.execution_counts.unrelated_actions,
        verdict.phase_timings.artifact.get(),
        verdict.phase_timings.engine_startup.get(),
        verdict.phase_timings.rust_installation.get(),
        verdict.phase_timings.security_scan.get(),
        verdict.phase_timings.case_execution.get(),
        verdict.phase_timings.cleanup.get(),
        verdict.phase_timings.total.get(),
        verdict.cases.len(),
    );
    rendered.push_str(&format!(
        "\n## Closure domains\n\n| Domain | Identity or result |\n| --- | --- |\n| Applicability and case catalog | `{}` |\n| Implementation closure | `{}` |\n| Portable platform closure | `{}` (`{}`) |\n| Security closure | `{}` (`{}`) |\n| Exact artifact manifest | `{}` |\n| Exact artifact payload | `{}` |\n| Artifact Import receipt | `{}` |\n| Exact engine | `{}` |\n| Installed Rust baseline | `{}` |\n| Stable connector | `{}` |\n",
        verdict.case_catalog_digest,
        verdict.closure_bundle_digest,
        verdict.platform_matrix_digest,
        gate_label(verdict.platform_gate_passed),
        verdict.security_report_digest,
        gate_label(verdict.security_gate_passed),
        verdict.artifact_manifest_digest,
        verdict.artifact_payload_digest,
        verdict.artifact_import_receipt.receipt_digest,
        verdict.engine_identity_digest,
        verdict.baseline_digest,
        verdict.stable_connector.observation_digest,
    ));
    rendered.push_str("\n## Artifact component builds\n\n| Component | Count |\n| --- | ---: |\n");
    for (component, count) in &verdict.execution_counts.artifact.component_builds {
        rendered.push_str(&format!("| `{component:?}` | {count} |\n"));
    }
    rendered.push_str(
        "\n## Case attempts\n\n| Case | Attempts | Final outcome | Milliseconds |\n| --- | ---: | --- | ---: |\n",
    );
    for (case_id, observation) in &verdict.cases {
        rendered.push_str(&format!(
            "| `{case_id}` | {} | `{}` | {} |\n",
            observation.attempts.len(),
            outcome_label(&observation.final_outcome),
            observation.elapsed_millis.get(),
        ));
    }
    if let VerdictDecision::Failed { diagnostics } = &verdict.decision {
        rendered.push_str("\n## Diagnostics\n\n");
        for diagnostic in diagnostics.as_slice() {
            rendered.push_str(&format!(
                "- `{}`: {}\n",
                diagnostic.code,
                diagnostic.detail.as_str()
            ));
        }
    }
    rendered
}

const fn gate_label(passed: bool) -> &'static str {
    if passed { "passed" } else { "failed" }
}

const fn outcome_label(outcome: &CaseAttemptOutcome) -> &'static str {
    match outcome {
        CaseAttemptOutcome::Passed { .. } => "passed",
        CaseAttemptOutcome::AssertionFailed { .. } => "assertion-failed",
        CaseAttemptOutcome::InfrastructureFailed { .. } => "infrastructure-failed",
    }
}

fn policy_for_id(id: &NetworkPolicyId) -> Option<SignoffNetworkPolicy> {
    match id.as_str() {
        "network/engine-only" => Some(SignoffNetworkPolicy::EngineOnly),
        "network/immutable-remote" => Some(SignoffNetworkPolicy::ImmutableRemote),
        "network/manifest-and-engine" => Some(SignoffNetworkPolicy::ManifestAndEngine),
        "network/read-only-public-dependencies" => {
            Some(SignoffNetworkPolicy::ReadOnlyPublicDependencies)
        }
        _ => None,
    }
}

fn validate_execution_counts(
    plan: &SignoffRunPlan,
    counts: &SignoffExecutionCounts,
    diagnostics: &mut Vec<ConformanceDiagnostic>,
) {
    if counts.preflight_smoke_engine_starts != 1
        || counts.orchestration_engine_starts != 1
        || counts.exact_target_engine_starts != 1
        || counts.exact_target_engine_stops != 1
        || counts.exact_target_child_reaps != 1
        || counts.rust_baseline_materializations != 1
        || counts.case_executions != plan.expected_case_executions.get()
        || counts.closure_replays != 0
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffEngineLifecycleInvalid,
            DiagnosticPhase::Engine,
            "engine baseline preflight or closure lifecycle count is not exact",
        ));
    }
    let build = matches!(
        plan.artifact_plan.materialization,
        ArtifactMaterialization::Build
    );
    let components_exact = counts.artifact.component_builds.len()
        == required_artifact_components().len()
        && required_artifact_components().into_iter().all(|component| {
            counts.artifact.component_builds.get(&component) == Some(&u32::from(build))
        });
    if counts.artifact.construction != u32::from(build)
        || counts.artifact.imports != u32::from(!build)
        || !components_exact
        || !counts.artifact.forbidden_work.is_empty()
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffDuplicateWork,
            DiagnosticPhase::Artifact,
            "artifact build import or component counters are not exact",
        ));
    }
}

fn validate_phase_timings(
    plan: &SignoffRunPlan,
    observation: &SignoffObservation,
    diagnostics: &mut Vec<ConformanceDiagnostic>,
) {
    let case_total = observation.cases.iter().try_fold(0_u64, |total, case| {
        total.checked_add(case.elapsed_millis.get())
    });
    let shared_total = [
        observation.phase_timings.artifact.get(),
        observation.phase_timings.engine_startup.get(),
        observation.phase_timings.rust_installation.get(),
        observation.phase_timings.security_scan.get(),
        observation.phase_timings.case_execution.get(),
        observation.phase_timings.cleanup.get(),
    ]
    .into_iter()
    .try_fold(0_u64, u64::checked_add);
    if case_total != Some(observation.phase_timings.case_execution.get())
        || shared_total != Some(observation.phase_timings.total.get())
        || observation.phase_timings.total.get() > plan.total_budget.get()
    {
        diagnostics.push(verdict_diagnostic(
            ConformanceDiagnosticCode::SignoffVerdictIncomplete,
            DiagnosticPhase::Verdict,
            "phase or case timings are incomplete inconsistent or over budget",
        ));
    }
}

fn verify_verdict_digest(verdict: &AtomicSignoffVerdict) -> Result<(), SignoffDecodeError> {
    let expected = canonical_digest(DigestDomain::ConformanceVerdict, &verdict.projection())?;
    if expected != verdict.verdict_digest {
        return Err(SignoffDecodeError::VerdictDigestMismatch);
    }
    Ok(())
}

fn verify_release_handoff_digest(handoff: &ReleaseHandoffRecord) -> Result<(), SignoffDecodeError> {
    let expected = canonical_digest(
        DigestDomain::ConformanceReleaseHandoff,
        &handoff.projection(),
    )?;
    if expected != handoff.handoff_digest {
        return Err(SignoffDecodeError::ReleaseHandoffDigestMismatch);
    }
    Ok(())
}

fn release_handoff_error() -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([verdict_diagnostic(
        ConformanceDiagnosticCode::SignoffReleaseHandoffInvalid,
        DiagnosticPhase::Verdict,
        "release handoff bytes scope or verdict are not identity exact",
    )])
    .expect("one diagnostic is present")
}

fn verdict_diagnostic(
    code: ConformanceDiagnosticCode,
    phase: DiagnosticPhase,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(phase),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn case_diagnostic(
    code: ConformanceDiagnosticCode,
    case_id: &SignoffCaseId,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Case),
            case_id: Some(case_id.clone()),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}
