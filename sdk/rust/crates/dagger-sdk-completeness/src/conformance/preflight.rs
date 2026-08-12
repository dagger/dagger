//! Provider-neutral admission for the infrastructure used by exact-target SDK sign-off.
//!
//! Preflight proves only that a selected host can support later work. Its closed plan cannot name
//! a provider, target source, SDK case, capability, or arbitrary command, and its record cannot be
//! promoted to conformance evidence. The runner preserves the smoke-engine stop obligation after
//! any post-start failure.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{Architecture, CanonicalSet, Digest, NonEmptyText, OperatingSystem};

use super::{
    ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, NetworkPolicyId, NonZeroBytes,
    NonZeroCount, NonZeroMillis, PlatformDescriptor, ProvenanceId,
};

const MAX_RETAINED_OUTPUT_BYTES: u64 = 1024 * 1024;

/// Ordered phases which every valid host profile budgets explicitly.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum HostPreflightPhase {
    ObserveHost,
    ObserveContainerDaemon,
    RoundTripPersistentCanary,
    RoundTripExportedPayload,
    ObserveCacheReuse,
    StartSmokeEngine,
    ProbeSmokeService,
    StopSmokeEngine,
    ScanRetainedOutput,
}

impl HostPreflightPhase {
    /// Complete phase set in mandatory execution order.
    pub const ALL: [Self; 9] = [
        Self::ObserveHost,
        Self::ObserveContainerDaemon,
        Self::RoundTripPersistentCanary,
        Self::RoundTripExportedPayload,
        Self::ObserveCacheReuse,
        Self::StartSmokeEngine,
        Self::ProbeSmokeService,
        Self::StopSmokeEngine,
        Self::ScanRetainedOutput,
    ];

    fn diagnostic_phase(self) -> DiagnosticPhase {
        match self {
            Self::ObserveHost => DiagnosticPhase::HostResources,
            Self::ObserveContainerDaemon => DiagnosticPhase::ContainerDaemon,
            Self::RoundTripPersistentCanary => DiagnosticPhase::PersistentCanary,
            Self::RoundTripExportedPayload => DiagnosticPhase::ExportImport,
            Self::ObserveCacheReuse => DiagnosticPhase::CacheReuse,
            Self::StartSmokeEngine => DiagnosticPhase::SmokeStart,
            Self::ProbeSmokeService => DiagnosticPhase::SmokeProbe,
            Self::StopSmokeEngine => DiagnosticPhase::SmokeStop,
            Self::ScanRetainedOutput => DiagnosticPhase::RetainedOutput,
        }
    }
}

/// Closed action set accepted by [`HostProbe`].
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum HostPreflightStep {
    ObserveHost,
    ObserveContainerDaemon,
    RoundTripPersistentCanary,
    RoundTripExportedPayload,
    ObserveCacheReuse,
    StartSmokeEngine,
    ProbeSmokeService,
    StopSmokeEngine,
    ScanRetainedOutput,
}

impl HostPreflightStep {
    /// Returns the phase whose budget and diagnostic coordinate own this action.
    pub const fn phase(self) -> HostPreflightPhase {
        match self {
            Self::ObserveHost => HostPreflightPhase::ObserveHost,
            Self::ObserveContainerDaemon => HostPreflightPhase::ObserveContainerDaemon,
            Self::RoundTripPersistentCanary => HostPreflightPhase::RoundTripPersistentCanary,
            Self::RoundTripExportedPayload => HostPreflightPhase::RoundTripExportedPayload,
            Self::ObserveCacheReuse => HostPreflightPhase::ObserveCacheReuse,
            Self::StartSmokeEngine => HostPreflightPhase::StartSmokeEngine,
            Self::ProbeSmokeService => HostPreflightPhase::ProbeSmokeService,
            Self::StopSmokeEngine => HostPreflightPhase::StopSmokeEngine,
            Self::ScanRetainedOutput => HostPreflightPhase::ScanRetainedOutput,
        }
    }

    /// Complete ordered plan. Keeping this constant closed prevents callers from smuggling an
    /// arbitrary provider command or target action into preflight.
    pub const ALL: [Self; 9] = [
        Self::ObserveHost,
        Self::ObserveContainerDaemon,
        Self::RoundTripPersistentCanary,
        Self::RoundTripExportedPayload,
        Self::ObserveCacheReuse,
        Self::StartSmokeEngine,
        Self::ProbeSmokeService,
        Self::StopSmokeEngine,
        Self::ScanRetainedOutput,
    ];
}

/// Reviewed container daemon requirements for the host.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ContainerDaemonPolicy {
    pub minimum_api_version: NonEmptyText,
    pub allowed_storage_drivers: CanonicalSet<NonEmptyText>,
    pub minimum_storage_bytes: NonZeroBytes,
    pub privileged_containers: bool,
}

/// Required persistence/export/cache semantics independent of one provider's implementation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PersistencePolicy {
    pub require_process_restart: bool,
    pub require_export_import: bool,
    pub require_cache_reuse: bool,
}

/// Checked host contract consumed by the private preflight binary.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SignoffHostProfile {
    pub format_version: ConformanceFormatVersion,
    pub profile_version: NonZeroCount,
    pub platform: PlatformDescriptor,
    pub minimum_cpu_count: NonZeroCount,
    pub minimum_memory_bytes: NonZeroBytes,
    pub minimum_workspace_bytes: NonZeroBytes,
    pub container_policy: ContainerDaemonPolicy,
    pub preflight_tool: ProvenanceId,
    pub smoke_tool: ProvenanceId,
    pub smoke_engine: ProvenanceId,
    pub persistence_policy: PersistencePolicy,
    pub network_policy: NetworkPolicyId,
    pub phase_budgets: BTreeMap<HostPreflightPhase, NonZeroMillis>,
}

/// Immutable typed plan. It contains only reviewed steps and a domain-separated profile identity.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HostPreflightPlan {
    pub format_version: ConformanceFormatVersion,
    pub profile: SignoffHostProfile,
    pub profile_digest: Digest,
    pub steps: Vec<HostPreflightStep>,
    pub plan_digest: Digest,
}

#[derive(Serialize)]
struct HostPlanDigestInput<'a> {
    format_version: ConformanceFormatVersion,
    profile_digest: &'a Digest,
    steps: &'a [HostPreflightStep],
}

/// Observed host resources after machine-local paths and provider metadata have been discarded.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HostResourceObservation {
    pub platform: PlatformDescriptor,
    pub cpu_count: NonZeroCount,
    pub memory_bytes: NonZeroBytes,
    pub workspace_bytes: NonZeroBytes,
}

/// Safe container daemon identity and capacity observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ContainerDaemonObservation {
    pub available: bool,
    pub api_version: NonEmptyText,
    pub storage_driver: NonEmptyText,
    pub storage_bytes: NonZeroBytes,
    pub privileged_containers: bool,
    pub daemon_identity: Digest,
}

/// Stable result emitted by one closed preflight step.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum HostStepResult {
    HostResources {
        observation: HostResourceObservation,
    },
    ContainerDaemon {
        observation: ContainerDaemonObservation,
    },
    PersistentCanary {
        before: Digest,
        after_restart: Digest,
        restart_count: NonZeroCount,
    },
    ExportedPayload {
        exported: Digest,
        imported: Digest,
    },
    CacheReuse {
        first_output: Digest,
        second_output: Digest,
        reused: bool,
    },
    SmokeStarted {
        smoke_tool: ProvenanceId,
        smoke_engine: ProvenanceId,
        start_count: NonZeroCount,
    },
    SmokeServiceProbed {
        reachable: bool,
        probe_count: NonZeroCount,
    },
    SmokeStopped {
        stopped: bool,
        reaped: bool,
        stop_count: NonZeroCount,
    },
    RetainedOutputScanned {
        inspected_bytes: u64,
        canary_matches: u32,
    },
}

/// One phase-bound observation. The result variant must correspond exactly to `step`.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HostStepObservation {
    pub step: HostPreflightStep,
    pub elapsed: NonZeroMillis,
    pub result: HostStepResult,
}

/// Event classes which are categorically outside infrastructure preflight.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ForbiddenPreflightEvent {
    ExactTargetArtifactBuild,
    ExactTargetCliBuild,
    RustSdkInstall,
    CaseExecution,
    CapabilityClaim,
}

/// Complete raw typed observation supplied to admission.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HostPreflightObservation {
    pub format_version: ConformanceFormatVersion,
    pub profile_digest: Digest,
    pub plan_digest: Digest,
    pub steps: Vec<HostStepObservation>,
    pub forbidden_events: CanonicalSet<ForbiddenPreflightEvent>,
}

/// Canonical infrastructure-only record safe to retain and reuse by digest.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HostPreflightRecord {
    pub format_version: ConformanceFormatVersion,
    pub profile_digest: Digest,
    pub plan_digest: Digest,
    pub platform: PlatformDescriptor,
    pub cpu_count: NonZeroCount,
    pub memory_bytes: NonZeroBytes,
    pub workspace_bytes: NonZeroBytes,
    pub container_daemon: ContainerDaemonObservation,
    pub preflight_tool: ProvenanceId,
    pub smoke_tool: ProvenanceId,
    pub smoke_engine: ProvenanceId,
    pub smoke_start_count: NonZeroCount,
    pub smoke_probe_count: NonZeroCount,
    pub smoke_stop_count: NonZeroCount,
    pub persistent_canary_digest: Digest,
    pub exported_payload_digest: Digest,
    pub cache_output_digest: Digest,
    pub phase_timings: BTreeMap<HostPreflightPhase, NonZeroMillis>,
    pub retained_output_bytes: u64,
    pub record_digest: Digest,
}

#[derive(Serialize)]
struct HostRecordDigestInput<'a> {
    format_version: ConformanceFormatVersion,
    profile_digest: &'a Digest,
    plan_digest: &'a Digest,
    platform: &'a PlatformDescriptor,
    cpu_count: NonZeroCount,
    memory_bytes: NonZeroBytes,
    workspace_bytes: NonZeroBytes,
    container_daemon: &'a ContainerDaemonObservation,
    preflight_tool: &'a ProvenanceId,
    smoke_tool: &'a ProvenanceId,
    smoke_engine: &'a ProvenanceId,
    smoke_start_count: NonZeroCount,
    smoke_probe_count: NonZeroCount,
    smoke_stop_count: NonZeroCount,
    persistent_canary_digest: &'a Digest,
    exported_payload_digest: &'a Digest,
    cache_output_digest: &'a Digest,
    phase_timings: &'a BTreeMap<HostPreflightPhase, NonZeroMillis>,
    retained_output_bytes: u64,
}

/// Safe operational failure class returned by a host adapter.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum HostProbeErrorKind {
    Unavailable,
    TimedOut,
    InvalidOutput,
    CleanupFailed,
}

/// Adapter failure which deliberately excludes raw process output and paths.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct HostProbeError {
    pub step: HostPreflightStep,
    pub kind: HostProbeErrorKind,
}

/// Typed adapter boundary implemented by both the process host and deterministic fixtures.
pub trait HostProbe {
    fn observe(&mut self, step: &HostPreflightStep) -> Result<HostStepObservation, HostProbeError>;
}

/// Validates repository policy and produces the one closed provider-neutral plan.
pub fn plan_host_preflight(
    profile: SignoffHostProfile,
) -> Result<HostPreflightPlan, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    if profile.format_version != ConformanceFormatVersion::V1
        || profile.profile_version.get() != 1
        || profile.platform != PlatformDescriptor::linux_amd64()
    {
        diagnostics.push(profile_diagnostic(
            "host profile version or platform is unsupported",
        ));
    }
    if profile.container_policy.allowed_storage_drivers.is_empty()
        || !profile.container_policy.privileged_containers
        || !profile.persistence_policy.require_process_restart
        || !profile.persistence_policy.require_export_import
        || !profile.persistence_policy.require_cache_reuse
    {
        diagnostics.push(profile_diagnostic(
            "host profile omits a required infrastructure boundary",
        ));
    }
    let phases = profile
        .phase_budgets
        .keys()
        .copied()
        .collect::<BTreeSet<_>>();
    let required = HostPreflightPhase::ALL.into_iter().collect::<BTreeSet<_>>();
    if phases != required {
        diagnostics.push(profile_diagnostic(
            "host profile phase budgets are not exact",
        ));
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }

    let profile_digest = canonical_digest(DigestDomain::ConformanceHostProfile, &profile)
        .map_err(|_| one_profile_diagnostic("host profile cannot be encoded canonically"))?;
    let steps = HostPreflightStep::ALL.to_vec();
    let plan_digest = canonical_digest(
        DigestDomain::ConformanceHostPlan,
        &HostPlanDigestInput {
            format_version: profile.format_version,
            profile_digest: &profile_digest,
            steps: &steps,
        },
    )
    .map_err(|_| one_profile_diagnostic("host plan cannot be encoded canonically"))?;
    Ok(HostPreflightPlan {
        format_version: profile.format_version,
        profile,
        profile_digest,
        steps,
        plan_digest,
    })
}

/// Runs one plan and guarantees a smoke stop attempt after a successful start observation.
pub fn run_host_preflight<P: HostProbe>(
    plan: &HostPreflightPlan,
    probe: &mut P,
) -> Result<HostPreflightRecord, ConformanceDiagnosticSet> {
    let mut observations = Vec::new();
    let mut errors = Vec::new();
    let mut smoke_started = false;
    let mut failed = false;

    for step in &plan.steps {
        let cleanup_step = matches!(
            step,
            HostPreflightStep::StopSmokeEngine | HostPreflightStep::ScanRetainedOutput
        );
        if failed && !(smoke_started && cleanup_step) {
            continue;
        }
        match probe.observe(step) {
            Ok(observation) => {
                if matches!(step, HostPreflightStep::StartSmokeEngine)
                    && matches!(observation.result, HostStepResult::SmokeStarted { .. })
                {
                    smoke_started = true;
                }
                observations.push(observation);
            }
            Err(error) => {
                failed = true;
                let cleanup = matches!(step, HostPreflightStep::StopSmokeEngine)
                    || error.kind == HostProbeErrorKind::CleanupFailed;
                errors.push(ConformanceDiagnostic::new(
                    ConformanceDiagnosticCode::SignoffHostPreflightFailed,
                    DiagnosticCoordinate {
                        phase: Some(if cleanup {
                            DiagnosticPhase::Cleanup
                        } else {
                            error.step.phase().diagnostic_phase()
                        }),
                        ..DiagnosticCoordinate::default()
                    },
                    if cleanup {
                        "preflight cleanup failed"
                    } else {
                        "preflight host probe failed"
                    },
                ));
            }
        }
    }
    if let Some(set) = ConformanceDiagnosticSet::new(errors) {
        return Err(set);
    }
    admit_host_preflight(
        plan,
        HostPreflightObservation {
            format_version: plan.format_version,
            profile_digest: plan.profile_digest.clone(),
            plan_digest: plan.plan_digest.clone(),
            steps: observations,
            forbidden_events: CanonicalSet::default(),
        },
    )
}

/// Admits a complete infrastructure observation without creating any SDK capability claim.
pub fn admit_host_preflight(
    plan: &HostPreflightPlan,
    observation: HostPreflightObservation,
) -> Result<HostPreflightRecord, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    if observation.format_version != plan.format_version
        || observation.profile_digest != plan.profile_digest
        || observation.plan_digest != plan.plan_digest
    {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostPreflightStale,
            phase_coordinate(DiagnosticPhase::HostProfile),
            "preflight observation identity is stale",
        ));
    }
    if !observation.forbidden_events.is_empty() {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffUnrelatedWork,
            phase_coordinate(DiagnosticPhase::HostProfile),
            "preflight contains target or conformance work",
        ));
    }
    if observation.steps.len() != plan.steps.len()
        || observation
            .steps
            .iter()
            .map(|item| item.step)
            .ne(plan.steps.iter().copied())
    {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostPreflightFailed,
            phase_coordinate(DiagnosticPhase::HostProfile),
            "preflight step sequence is incomplete or reordered",
        ));
    }

    let mut phase_timings = BTreeMap::new();
    for item in &observation.steps {
        let phase = item.step.phase();
        let budget = plan.profile.phase_budgets.get(&phase);
        if budget.is_none_or(|budget| item.elapsed > *budget) {
            diagnostics.push(ConformanceDiagnostic::new(
                ConformanceDiagnosticCode::SignoffHostPreflightFailed,
                phase_coordinate(phase.diagnostic_phase()),
                "preflight phase exceeded its declared budget",
            ));
        }
        phase_timings.insert(phase, item.elapsed);
        if !result_matches_step(item.step, &item.result) {
            diagnostics.push(ConformanceDiagnostic::new(
                ConformanceDiagnosticCode::SignoffHostPreflightFailed,
                phase_coordinate(phase.diagnostic_phase()),
                "preflight step returned the wrong typed observation",
            ));
        }
    }

    let host = find_result(&observation.steps, HostPreflightStep::ObserveHost).and_then(|result| {
        match result {
            HostStepResult::HostResources { observation } => Some(observation),
            _ => None,
        }
    });
    let daemon = find_result(
        &observation.steps,
        HostPreflightStep::ObserveContainerDaemon,
    )
    .and_then(|result| match result {
        HostStepResult::ContainerDaemon { observation } => Some(observation),
        _ => None,
    });

    if host.is_none_or(|host| {
        host.platform != plan.profile.platform
            || host.cpu_count < plan.profile.minimum_cpu_count
            || host.memory_bytes < plan.profile.minimum_memory_bytes
            || host.workspace_bytes < plan.profile.minimum_workspace_bytes
    }) {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostPreflightFailed,
            phase_coordinate(DiagnosticPhase::HostResources),
            "host platform or resources do not satisfy the profile",
        ));
    }
    if daemon.is_none_or(|daemon| !daemon_satisfies(daemon, &plan.profile.container_policy)) {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostPreflightFailed,
            phase_coordinate(DiagnosticPhase::ContainerDaemon),
            "container daemon does not satisfy the profile",
        ));
    }

    let persistent = find_result(
        &observation.steps,
        HostPreflightStep::RoundTripPersistentCanary,
    )
    .and_then(|result| match result {
        HostStepResult::PersistentCanary {
            before,
            after_restart,
            restart_count,
        } if before == after_restart && restart_count.get() == 1 => Some(before),
        _ => None,
    });
    let exported = find_result(
        &observation.steps,
        HostPreflightStep::RoundTripExportedPayload,
    )
    .and_then(|result| match result {
        HostStepResult::ExportedPayload { exported, imported } if exported == imported => {
            Some(exported)
        }
        _ => None,
    });
    let cache =
        find_result(&observation.steps, HostPreflightStep::ObserveCacheReuse).and_then(|result| {
            match result {
                HostStepResult::CacheReuse {
                    first_output,
                    second_output,
                    reused,
                } if first_output == second_output && *reused => Some(first_output),
                _ => None,
            }
        });
    if persistent.is_none() || exported.is_none() || cache.is_none() {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostBoundaryInvalid,
            phase_coordinate(DiagnosticPhase::PersistentCanary),
            "persistence export or cache canary did not round trip",
        ));
    }

    let started =
        find_result(&observation.steps, HostPreflightStep::StartSmokeEngine).and_then(|result| {
            match result {
                HostStepResult::SmokeStarted {
                    smoke_tool,
                    smoke_engine,
                    start_count,
                } if smoke_tool == &plan.profile.smoke_tool
                    && smoke_engine == &plan.profile.smoke_engine
                    && start_count.get() == 1 =>
                {
                    Some((smoke_tool, smoke_engine, *start_count))
                }
                _ => None,
            }
        });
    let probed =
        find_result(&observation.steps, HostPreflightStep::ProbeSmokeService).and_then(|result| {
            match result {
                HostStepResult::SmokeServiceProbed {
                    reachable: true,
                    probe_count,
                } if probe_count.get() == 1 => Some(*probe_count),
                _ => None,
            }
        });
    let stopped =
        find_result(&observation.steps, HostPreflightStep::StopSmokeEngine).and_then(|result| {
            match result {
                HostStepResult::SmokeStopped {
                    stopped: true,
                    reaped: true,
                    stop_count,
                } if stop_count.get() == 1 => Some(*stop_count),
                _ => None,
            }
        });
    if started.is_none() || probed.is_none() || stopped.is_none() {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostSmokeInvalid,
            phase_coordinate(DiagnosticPhase::SmokeProbe),
            "smoke lifecycle is incomplete unreachable or unreaped",
        ));
    }

    let retained_bytes = find_result(&observation.steps, HostPreflightStep::ScanRetainedOutput)
        .and_then(|result| match result {
            HostStepResult::RetainedOutputScanned {
                inspected_bytes,
                canary_matches: 0,
            } => (*inspected_bytes <= MAX_RETAINED_OUTPUT_BYTES).then_some(*inspected_bytes),
            _ => None,
        });
    if retained_bytes.is_none() {
        diagnostics.push(ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::EvidenceRedactionFailed,
            phase_coordinate(DiagnosticPhase::RetainedOutput),
            "retained preflight output contains unsafe canary data",
        ));
    }

    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }

    // All required observations are proven above; destructuring after the fail-closed boundary
    // keeps a partially valid record structurally impossible.
    let host = host.expect("validated host observation exists");
    let daemon = daemon.expect("validated daemon observation exists");
    let (smoke_tool, smoke_engine, start_count) = started.expect("validated smoke start exists");
    let probe_count = probed.expect("validated smoke probe exists");
    let stop_count = stopped.expect("validated smoke stop exists");
    let persistent = persistent.expect("validated persistent canary exists");
    let exported = exported.expect("validated export canary exists");
    let cache = cache.expect("validated cache canary exists");
    let retained_bytes = retained_bytes.expect("validated retained output scan exists");
    let digest_input = HostRecordDigestInput {
        format_version: plan.format_version,
        profile_digest: &plan.profile_digest,
        plan_digest: &plan.plan_digest,
        platform: &host.platform,
        cpu_count: host.cpu_count,
        memory_bytes: host.memory_bytes,
        workspace_bytes: host.workspace_bytes,
        container_daemon: daemon,
        preflight_tool: &plan.profile.preflight_tool,
        smoke_tool,
        smoke_engine,
        smoke_start_count: start_count,
        smoke_probe_count: probe_count,
        smoke_stop_count: stop_count,
        persistent_canary_digest: persistent,
        exported_payload_digest: exported,
        cache_output_digest: cache,
        phase_timings: &phase_timings,
        retained_output_bytes: retained_bytes,
    };
    let record_digest = canonical_digest(DigestDomain::ConformanceHostRecord, &digest_input)
        .map_err(|_| one_profile_diagnostic("preflight record cannot be encoded canonically"))?;
    Ok(HostPreflightRecord {
        format_version: plan.format_version,
        profile_digest: plan.profile_digest.clone(),
        plan_digest: plan.plan_digest.clone(),
        platform: host.platform.clone(),
        cpu_count: host.cpu_count,
        memory_bytes: host.memory_bytes,
        workspace_bytes: host.workspace_bytes,
        container_daemon: daemon.clone(),
        preflight_tool: plan.profile.preflight_tool.clone(),
        smoke_tool: smoke_tool.clone(),
        smoke_engine: smoke_engine.clone(),
        smoke_start_count: start_count,
        smoke_probe_count: probe_count,
        smoke_stop_count: stop_count,
        persistent_canary_digest: persistent.clone(),
        exported_payload_digest: exported.clone(),
        cache_output_digest: cache.clone(),
        phase_timings,
        retained_output_bytes: retained_bytes,
        record_digest,
    })
}

/// Scans bounded output chunks without allowing a canary split at chunk boundaries to evade
/// detection. The caller records only the byte and match counts; raw output remains ephemeral.
pub fn scan_retained_output<'a>(
    chunks: impl IntoIterator<Item = &'a [u8]>,
    canaries: &[&[u8]],
) -> Result<(u64, u32), &'static str> {
    if canaries.iter().any(|canary| canary.is_empty()) {
        return Err("secret canaries must be non-empty");
    }
    let mut output = Vec::new();
    for chunk in chunks {
        let next_len = output.len().saturating_add(chunk.len());
        if next_len > MAX_RETAINED_OUTPUT_BYTES as usize {
            return Err("retained output exceeds the preflight bound");
        }
        output.extend_from_slice(chunk);
    }
    let matches = canaries
        .iter()
        .filter(|canary| {
            output
                .windows(canary.len())
                .any(|window| window == **canary)
        })
        .count();
    Ok((
        output.len() as u64,
        u32::try_from(matches).expect("bounded output cannot contain more than u32 canary inputs"),
    ))
}

/// Revalidates a retained record against the current immutable profile and daemon identity.
pub fn validate_host_preflight_record(
    plan: &HostPreflightPlan,
    record: &HostPreflightRecord,
    daemon_identity: &Digest,
) -> Result<(), ConformanceDiagnosticSet> {
    let digest_input = HostRecordDigestInput {
        format_version: record.format_version,
        profile_digest: &record.profile_digest,
        plan_digest: &record.plan_digest,
        platform: &record.platform,
        cpu_count: record.cpu_count,
        memory_bytes: record.memory_bytes,
        workspace_bytes: record.workspace_bytes,
        container_daemon: &record.container_daemon,
        preflight_tool: &record.preflight_tool,
        smoke_tool: &record.smoke_tool,
        smoke_engine: &record.smoke_engine,
        smoke_start_count: record.smoke_start_count,
        smoke_probe_count: record.smoke_probe_count,
        smoke_stop_count: record.smoke_stop_count,
        persistent_canary_digest: &record.persistent_canary_digest,
        exported_payload_digest: &record.exported_payload_digest,
        cache_output_digest: &record.cache_output_digest,
        phase_timings: &record.phase_timings,
        retained_output_bytes: record.retained_output_bytes,
    };
    let recomputed = canonical_digest(DigestDomain::ConformanceHostRecord, &digest_input).ok();
    let exact_timings = record.phase_timings.len() == HostPreflightPhase::ALL.len()
        && HostPreflightPhase::ALL.iter().all(|phase| {
            record
                .phase_timings
                .get(phase)
                .zip(plan.profile.phase_budgets.get(phase))
                .is_some_and(|(elapsed, budget)| elapsed <= budget)
        });
    let valid = record.format_version == plan.format_version
        && record.profile_digest == plan.profile_digest
        && record.plan_digest == plan.plan_digest
        && &record.container_daemon.daemon_identity == daemon_identity
        && record.record_digest == recomputed.expect("the validated record model is canonical")
        && record.platform == plan.profile.platform
        && record.cpu_count >= plan.profile.minimum_cpu_count
        && record.memory_bytes >= plan.profile.minimum_memory_bytes
        && record.workspace_bytes >= plan.profile.minimum_workspace_bytes
        && daemon_satisfies(&record.container_daemon, &plan.profile.container_policy)
        && record.preflight_tool == plan.profile.preflight_tool
        && record.smoke_tool == plan.profile.smoke_tool
        && record.smoke_engine == plan.profile.smoke_engine
        && record.smoke_start_count.get() == 1
        && record.smoke_probe_count.get() == 1
        && record.smoke_stop_count.get() == 1
        && exact_timings;
    if valid {
        Ok(())
    } else {
        Err(ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
            ConformanceDiagnosticCode::SignoffHostPreflightStale,
            phase_coordinate(DiagnosticPhase::HostProfile),
            "host profile plan or daemon identity changed",
        )])
        .expect("one diagnostic is non-empty"))
    }
}

fn result_matches_step(step: HostPreflightStep, result: &HostStepResult) -> bool {
    matches!(
        (step, result),
        (
            HostPreflightStep::ObserveHost,
            HostStepResult::HostResources { .. }
        ) | (
            HostPreflightStep::ObserveContainerDaemon,
            HostStepResult::ContainerDaemon { .. }
        ) | (
            HostPreflightStep::RoundTripPersistentCanary,
            HostStepResult::PersistentCanary { .. }
        ) | (
            HostPreflightStep::RoundTripExportedPayload,
            HostStepResult::ExportedPayload { .. }
        ) | (
            HostPreflightStep::ObserveCacheReuse,
            HostStepResult::CacheReuse { .. }
        ) | (
            HostPreflightStep::StartSmokeEngine,
            HostStepResult::SmokeStarted { .. }
        ) | (
            HostPreflightStep::ProbeSmokeService,
            HostStepResult::SmokeServiceProbed { .. }
        ) | (
            HostPreflightStep::StopSmokeEngine,
            HostStepResult::SmokeStopped { .. }
        ) | (
            HostPreflightStep::ScanRetainedOutput,
            HostStepResult::RetainedOutputScanned { .. }
        )
    )
}

fn find_result(
    observations: &[HostStepObservation],
    step: HostPreflightStep,
) -> Option<&HostStepResult> {
    observations
        .iter()
        .find(|item| item.step == step)
        .map(|item| &item.result)
}

fn daemon_satisfies(
    observation: &ContainerDaemonObservation,
    policy: &ContainerDaemonPolicy,
) -> bool {
    observation.available
        && api_version_at_least(
            observation.api_version.as_str(),
            policy.minimum_api_version.as_str(),
        )
        && policy
            .allowed_storage_drivers
            .iter()
            .any(|driver| driver == &observation.storage_driver)
        && observation.storage_bytes >= policy.minimum_storage_bytes
        && observation.privileged_containers == policy.privileged_containers
}

fn api_version_at_least(observed: &str, minimum: &str) -> bool {
    fn components(value: &str) -> Option<(u32, u32)> {
        let (major, minor) = value.split_once('.')?;
        Some((major.parse().ok()?, minor.parse().ok()?))
    }
    matches!((components(observed), components(minimum)), (Some(observed), Some(minimum)) if observed >= minimum)
}

fn phase_coordinate(phase: DiagnosticPhase) -> DiagnosticCoordinate {
    DiagnosticCoordinate {
        phase: Some(phase),
        ..DiagnosticCoordinate::default()
    }
}

fn profile_diagnostic(detail: &'static str) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::SignoffHostProfileInvalid,
        phase_coordinate(DiagnosticPhase::HostProfile),
        detail,
    )
}

fn one_profile_diagnostic(detail: &'static str) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([profile_diagnostic(detail)])
        .expect("one diagnostic is non-empty")
}

/// Returns the reviewed first-host policy bound to an exact private preflight binary.
pub fn linux_amd64_host_profile(preflight_tool: ProvenanceId) -> SignoffHostProfile {
    let gib = 1024_u64.pow(3);
    SignoffHostProfile {
        format_version: ConformanceFormatVersion::V1,
        profile_version: NonZeroCount::new(1).expect("one is non-zero"),
        platform: PlatformDescriptor {
            operating_system: OperatingSystem::Linux,
            architecture: Architecture::Amd64,
        },
        minimum_cpu_count: NonZeroCount::new(16).expect("reviewed count is valid"),
        minimum_memory_bytes: NonZeroBytes::new(48 * gib).expect("reviewed capacity is valid"),
        minimum_workspace_bytes: NonZeroBytes::new(160 * gib)
            .expect("reviewed capacity is valid"),
        container_policy: ContainerDaemonPolicy {
            minimum_api_version: NonEmptyText::new("1.44").expect("reviewed API is valid"),
            allowed_storage_drivers: CanonicalSet::new([
                NonEmptyText::new("overlay2").expect("reviewed driver is valid"),
                NonEmptyText::new("overlayfs").expect("reviewed driver is valid"),
            ]),
            minimum_storage_bytes: NonZeroBytes::new(160 * gib)
                .expect("reviewed capacity is valid"),
            privileged_containers: true,
        },
        preflight_tool,
        smoke_tool: ProvenanceId::new(
            "tool/docker/29.3.0/sha256/b803740c076b46942159eab6ab7a5678ec6e4e3beec330487f5984fa02c06e10",
        )
        .expect("reviewed identity is valid"),
        smoke_engine: ProvenanceId::new(
            "oci/registry.dagger.io/engine/v1.0.0-beta.9/sha256/de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e",
        )
        .expect("reviewed identity is valid"),
        persistence_policy: PersistencePolicy {
            require_process_restart: true,
            require_export_import: true,
            require_cache_reuse: true,
        },
        network_policy: NetworkPolicyId::new("network/isolated-smoke-service")
            .expect("reviewed identity is valid"),
        phase_budgets: HostPreflightPhase::ALL
            .into_iter()
            .map(|phase| {
                let millis = match phase {
                    HostPreflightPhase::StartSmokeEngine => 180_000,
                    HostPreflightPhase::ProbeSmokeService => 120_000,
                    HostPreflightPhase::RoundTripExportedPayload => 180_000,
                    _ => 60_000,
                };
                (phase, NonZeroMillis::new(millis).expect("reviewed budget is valid"))
            })
            .collect(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture_profile() -> SignoffHostProfile {
        linux_amd64_host_profile(
            ProvenanceId::new(
                "binary/dagger-rust-sdk-signoff/sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            )
            .unwrap(),
        )
    }

    #[test]
    fn plan_is_closed_provider_neutral_and_canonical() {
        let plan = plan_host_preflight(fixture_profile()).unwrap();
        assert_eq!(plan.steps, HostPreflightStep::ALL);
        let bytes = crate::canonical::canonical_bytes(&plan).unwrap();
        let text = String::from_utf8(bytes).unwrap();
        for forbidden in ["namespace", "devbox", "/Users/", "account", "credential"] {
            assert!(
                !text
                    .to_ascii_lowercase()
                    .contains(&forbidden.to_ascii_lowercase())
            );
        }
    }

    #[test]
    fn changed_daemon_identity_invalidates_record() {
        let plan = plan_host_preflight(fixture_profile()).unwrap();
        let mut fixture = FixtureProbe::valid(&plan);
        let record = run_host_preflight(&plan, &mut fixture).unwrap();
        assert!(
            validate_host_preflight_record(&plan, &record, &Digest::sha256("another daemon"))
                .is_err()
        );
    }

    #[derive(Clone)]
    struct FixtureProbe {
        observations: BTreeMap<HostPreflightStep, HostStepObservation>,
    }

    impl FixtureProbe {
        fn valid(plan: &HostPreflightPlan) -> Self {
            let elapsed = NonZeroMillis::new(1).unwrap();
            let canary = Digest::sha256("canary");
            let daemon = ContainerDaemonObservation {
                available: true,
                api_version: NonEmptyText::new("1.52").unwrap(),
                storage_driver: NonEmptyText::new("overlayfs").unwrap(),
                storage_bytes: NonZeroBytes::new(200 * 1024_u64.pow(3)).unwrap(),
                privileged_containers: true,
                daemon_identity: Digest::sha256("daemon"),
            };
            let result = |step| match step {
                HostPreflightStep::ObserveHost => HostStepResult::HostResources {
                    observation: HostResourceObservation {
                        platform: PlatformDescriptor::linux_amd64(),
                        cpu_count: NonZeroCount::new(32).unwrap(),
                        memory_bytes: NonZeroBytes::new(64 * 1024_u64.pow(3)).unwrap(),
                        workspace_bytes: NonZeroBytes::new(198 * 1024_u64.pow(3)).unwrap(),
                    },
                },
                HostPreflightStep::ObserveContainerDaemon => HostStepResult::ContainerDaemon {
                    observation: daemon.clone(),
                },
                HostPreflightStep::RoundTripPersistentCanary => HostStepResult::PersistentCanary {
                    before: canary.clone(),
                    after_restart: canary.clone(),
                    restart_count: NonZeroCount::new(1).unwrap(),
                },
                HostPreflightStep::RoundTripExportedPayload => HostStepResult::ExportedPayload {
                    exported: canary.clone(),
                    imported: canary.clone(),
                },
                HostPreflightStep::ObserveCacheReuse => HostStepResult::CacheReuse {
                    first_output: canary.clone(),
                    second_output: canary.clone(),
                    reused: true,
                },
                HostPreflightStep::StartSmokeEngine => HostStepResult::SmokeStarted {
                    smoke_tool: plan.profile.smoke_tool.clone(),
                    smoke_engine: plan.profile.smoke_engine.clone(),
                    start_count: NonZeroCount::new(1).unwrap(),
                },
                HostPreflightStep::ProbeSmokeService => HostStepResult::SmokeServiceProbed {
                    reachable: true,
                    probe_count: NonZeroCount::new(1).unwrap(),
                },
                HostPreflightStep::StopSmokeEngine => HostStepResult::SmokeStopped {
                    stopped: true,
                    reaped: true,
                    stop_count: NonZeroCount::new(1).unwrap(),
                },
                HostPreflightStep::ScanRetainedOutput => HostStepResult::RetainedOutputScanned {
                    inspected_bytes: 0,
                    canary_matches: 0,
                },
            };
            Self {
                observations: plan
                    .steps
                    .iter()
                    .copied()
                    .map(|step| {
                        (
                            step,
                            HostStepObservation {
                                step,
                                elapsed,
                                result: result(step),
                            },
                        )
                    })
                    .collect(),
            }
        }
    }

    impl HostProbe for FixtureProbe {
        fn observe(
            &mut self,
            step: &HostPreflightStep,
        ) -> Result<HostStepObservation, HostProbeError> {
            Ok(self.observations.get(step).unwrap().clone())
        }
    }
}
