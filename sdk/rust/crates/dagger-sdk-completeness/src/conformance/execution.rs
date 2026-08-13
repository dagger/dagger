//! Exact installed-baseline, connector, case-execution, and fan-out admission.
//!
//! This layer treats graph objects as untrusted observations. An exact artifact becomes usable
//! only after its engine, CLI, packaged Rust descriptor, one installation, namespaces, attempt
//! history, and cleanup are bound into canonical identities. Retry is deliberately narrower than
//! failure: assertion failures are terminal, while the three admitted infrastructure classes may
//! create a fresh isolated attempt without rebuilding shared work.

#![warn(missing_docs)]

use std::collections::BTreeSet;

use serde::{Deserialize, Serialize};

use crate::canonical::{CanonicalError, DigestDomain, canonical_digest, decode_canonical};
use crate::model::{CommitSha, DaggerVersion, Digest, TargetDigest};

use super::{
    AdmittedArtifact, ArtifactComponent, CaseCatalog, CaseDefinition, ConformanceDiagnostic,
    ConformanceDiagnosticCode, ConformanceDiagnosticSet, DiagnosticCoordinate, DiagnosticPhase,
    InfrastructureFailureClass, NonZeroCount, NonZeroMillis, PlatformDescriptor, SignoffCaseId,
    SubjectIdentity,
};

/// Exact engine identity proved before an engine service may be started.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExactEngineIdentity {
    /// Exact target descriptor selected for sign-off.
    pub target_descriptor_digest: TargetDigest,
    /// Full engine and CLI source revision.
    pub target_revision: CommitSha,
    /// Exact semantic engine version expected by the SDK.
    pub engine_version: DaggerVersion,
    /// Platform of the retained exact-target artifact.
    pub platform: PlatformDescriptor,
    /// Rust workspace manifest contained in the target.
    pub rust_manifest_digest: Digest,
    /// Rust SDK dependency descriptor contained in the target.
    pub rust_descriptor_digest: Digest,
    /// Canonical identity over every preceding engine field.
    pub identity_digest: Digest,
}

/// Installed dependency source observed after `dagger sdk install --here rust`.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "source", deny_unknown_fields)]
pub enum DependencyDescriptorObservation {
    /// A published registry package selected by an exact descriptor.
    Registry {
        /// Canonical descriptor bytes.
        descriptor_digest: Digest,
    },
    /// An immutable Git package selected by a full revision.
    Git {
        /// Canonical descriptor bytes.
        descriptor_digest: Digest,
        /// Full immutable package revision.
        revision: CommitSha,
    },
    /// A checkout-relative dependency, retained only so admission can reject it.
    Path {
        /// Canonical descriptor bytes without retaining the unsafe host path.
        descriptor_digest: Digest,
    },
}

impl DependencyDescriptorObservation {
    fn digest(&self) -> &Digest {
        match self {
            Self::Registry { descriptor_digest }
            | Self::Git {
                descriptor_digest, ..
            }
            | Self::Path { descriptor_digest } => descriptor_digest,
        }
    }
}

/// Complete observation made while materializing the sole installed Rust baseline.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InstalledBaselineObservation {
    /// Engine identity independently observed from the imported or built target.
    pub engine: ExactEngineIdentity,
    /// Canonical exact-target manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Direct identity of the retained OCI payload bytes.
    pub artifact_payload_digest: Digest,
    /// Digest of the CLI extracted from the exact artifact.
    pub cli_digest: Digest,
    /// Digest-pinned clean runner image.
    pub runner_image_digest: Digest,
    /// Installed workspace configuration after the one install.
    pub installed_config_digest: Digest,
    /// Installed packaged dependency source.
    pub dependency: DependencyDescriptorObservation,
    /// Number of Rust SDK installation operations used to create the baseline.
    pub install_count: u32,
    /// Whether the workspace began as an initialized clean Git repository.
    pub clean_git_workspace: bool,
    /// Whether only the artifact CLI was made discoverable on `PATH`.
    pub artifact_cli_only_on_path: bool,
    /// Whether a host-provided Dagger CLI remained visible.
    pub host_cli_visible: bool,
    /// Whether pre-existing installed configuration survived into the baseline.
    pub stale_installed_config: bool,
    /// Engine service starts observed before identity validation completed.
    pub service_starts_before_validation: u32,
    /// Positive bounded materialization duration.
    pub elapsed_millis: NonZeroMillis,
}

/// One immutable installed Rust workspace from which all exact-engine cases branch.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InstalledRustBaseline {
    /// Canonical identity over the complete baseline.
    pub baseline_digest: Digest,
    /// Canonical portable artifact identity.
    pub artifact_digest: Digest,
    /// Exact artifact manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Exact retained OCI payload identity.
    pub artifact_payload_digest: Digest,
    /// Validated engine identity.
    pub engine: ExactEngineIdentity,
    /// Exact artifact CLI identity.
    pub cli_digest: Digest,
    /// Installed workspace configuration identity.
    pub installed_config_digest: Digest,
    /// Packaged registry or immutable-Git dependency descriptor identity.
    pub dependency_descriptor_digest: Digest,
    /// Clean digest-pinned runner image identity.
    pub runner_image_digest: Digest,
}

/// Compatibility HTTP status returned while resolving a production CLI manifest.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CompatibilityHttpStatus {
    /// The immutable release endpoint denied access.
    Forbidden,
    /// The immutable release endpoint has no manifest for the requested target.
    NotFound,
}

/// Production distribution-manifest result observed by the stable connector.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "outcome", deny_unknown_fields)]
pub enum DistributionManifestObservation {
    /// A checksum manifest and candidate CLI were returned.
    Available {
        /// Immutable checksum-manifest identity.
        manifest_digest: Digest,
        /// Candidate downloaded CLI identity.
        cli_digest: Digest,
        /// Whether the candidate bytes passed the production checksum verification.
        checksum_verified: bool,
    },
    /// Beta-target compatibility endpoint returned the only admitted unavailable statuses.
    Unavailable {
        /// Exact compatibility response class.
        status: CompatibilityHttpStatus,
    },
    /// A transport or response class outside the compatibility contract.
    OtherFailure,
}

/// CLI source selected by the production stable connector.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ConnectorCliSource {
    /// Checksum-verified CLI downloaded by the production distribution path.
    VerifiedDownload,
    /// Exact artifact CLI discovered through the compatibility `PATH` fallback.
    ArtifactPathFallback,
    /// Ambient host CLI, retained only so admission can reject substitution.
    Host,
}

/// Durable claim made for a successful stable-connector observation.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum DistributionClaim {
    /// Production downloaded and checksum-verified the selected CLI.
    VerifiedDownload,
    /// Beta compatibility used the exact artifact CLI already present on `PATH`.
    CompatibilityPathFallback,
}

/// Stable-connector facts recorded through the production distribution path.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StableConnectorObservation {
    /// Explicit local CLI override state; stable connector sign-off requires it to be unset.
    pub explicit_local_cli_selected: bool,
    /// Exact CLI made available on `PATH` before connector startup.
    pub path_cli_digest: Digest,
    /// Whether any ambient host CLI was discoverable.
    pub host_cli_visible: bool,
    /// Production manifest/download outcome.
    pub manifest: DistributionManifestObservation,
    /// Source ultimately selected by production connector code.
    pub selected_source: ConnectorCliSource,
    /// Exact selected CLI bytes.
    pub selected_cli_digest: Digest,
    /// Claim written into durable evidence.
    pub claim: DistributionClaim,
    /// Engine version returned through the connected session.
    pub observed_engine_version: DaggerVersion,
    /// Whether an authenticated Core query completed.
    pub authenticated_query_succeeded: bool,
    /// Explicit connector close operations.
    pub close_count: u32,
    /// Child-process reap observations after close.
    pub child_reap_count: u32,
    /// Positive bounded connector duration.
    pub elapsed_millis: NonZeroMillis,
}

/// Admitted connector result bound to one installed baseline.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AdmittedStableConnector {
    /// Installed baseline used by the connector.
    pub baseline_digest: Digest,
    /// Exact CLI selected by production code.
    pub selected_cli_digest: Digest,
    /// Honest distribution claim.
    pub claim: DistributionClaim,
    /// Canonical identity over the admitted observation.
    pub observation_digest: Digest,
}

/// Validates exact target identity and admits exactly one packaged Rust installation.
pub fn admit_installed_rust_baseline(
    artifact: &AdmittedArtifact,
    expected_engine_version: &DaggerVersion,
    observation: InstalledBaselineObservation,
) -> Result<InstalledRustBaseline, ConformanceDiagnosticSet> {
    let manifest = artifact.bundle().manifest();
    let cli_digest = manifest
        .components
        .get(&ArtifactComponent::Cli)
        .map(|component| &component.content_digest);
    let engine_digest = exact_engine_digest(&observation.engine)?;
    let engine_matches = observation.engine.identity_digest == engine_digest
        && observation.engine.target_descriptor_digest == manifest.target_descriptor_digest
        && observation.engine.target_revision == manifest.target_revision
        && observation.engine.engine_version == *expected_engine_version
        && observation.engine.platform == manifest.platform
        && observation.engine.rust_manifest_digest == manifest.rust_manifest_digest
        && observation.engine.rust_descriptor_digest == manifest.rust_descriptor_digest;
    let artifact_matches = observation.artifact_manifest_digest == *artifact.manifest_digest()
        && observation.artifact_payload_digest == *artifact.payload_digest()
        && cli_digest == Some(&observation.cli_digest);
    let install_is_exact = observation.install_count == 1
        && observation.clean_git_workspace
        && observation.artifact_cli_only_on_path
        && !observation.host_cli_visible
        && !observation.stale_installed_config
        && observation.service_starts_before_validation == 0
        && !matches!(
            observation.dependency,
            DependencyDescriptorObservation::Path { .. }
        );
    if !engine_matches || !artifact_matches || !install_is_exact {
        return Err(execution_error(
            ConformanceDiagnosticCode::SignoffRustBaselineInvalid,
            None,
            "installed baseline is stale path-backed duplicated or not exact-target",
        ));
    }

    let mut baseline = InstalledRustBaseline {
        baseline_digest: Digest::sha256([]),
        artifact_digest: artifact.bundle().bundle_digest().clone(),
        artifact_manifest_digest: observation.artifact_manifest_digest,
        artifact_payload_digest: observation.artifact_payload_digest,
        engine: observation.engine,
        cli_digest: observation.cli_digest,
        installed_config_digest: observation.installed_config_digest,
        dependency_descriptor_digest: observation.dependency.digest().clone(),
        runner_image_digest: observation.runner_image_digest,
    };
    baseline.baseline_digest = baseline_digest(&baseline)?;
    Ok(baseline)
}

/// Admits only a checksum-verified download or the exact beta compatibility fallback.
pub fn admit_stable_connector(
    baseline: &InstalledRustBaseline,
    observation: StableConnectorObservation,
) -> Result<AdmittedStableConnector, ConformanceDiagnosticSet> {
    if !baseline_is_self_consistent(baseline)
        || observation.explicit_local_cli_selected
        || observation.host_cli_visible
        || observation.path_cli_digest != baseline.cli_digest
        || observation.observed_engine_version != baseline.engine.engine_version
        || !observation.authenticated_query_succeeded
        || observation.close_count != 1
        || observation.child_reap_count != 1
    {
        return Err(execution_error(
            ConformanceDiagnosticCode::SignoffDistributionObservationInvalid,
            None,
            "connector lifecycle CLI identity or authenticated observation is invalid",
        ));
    }
    let honest = match (
        &observation.manifest,
        observation.selected_source,
        observation.claim,
    ) {
        (
            DistributionManifestObservation::Available {
                cli_digest,
                checksum_verified: true,
                ..
            },
            ConnectorCliSource::VerifiedDownload,
            DistributionClaim::VerifiedDownload,
        ) => cli_digest == &observation.selected_cli_digest,
        (
            DistributionManifestObservation::Unavailable {
                status: CompatibilityHttpStatus::Forbidden | CompatibilityHttpStatus::NotFound,
            },
            ConnectorCliSource::ArtifactPathFallback,
            DistributionClaim::CompatibilityPathFallback,
        ) => observation.selected_cli_digest == baseline.cli_digest,
        _ => false,
    };
    if !honest {
        return Err(execution_error(
            ConformanceDiagnosticCode::SignoffDistributionObservationInvalid,
            None,
            "distribution claim does not match verified download or compatibility fallback",
        ));
    }
    let observation_digest = canonical_digest(
        DigestDomain::ConformanceCaseExecution,
        &(&baseline.baseline_digest, &observation),
    )
    .map_err(|_| encoding_error(None))?;
    Ok(AdmittedStableConnector {
        baseline_digest: baseline.baseline_digest.clone(),
        selected_cli_digest: observation.selected_cli_digest,
        claim: observation.claim,
        observation_digest,
    })
}

/// Immutable identity shared by every attempt of one catalog case.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CaseExecutionBinding {
    /// Stable case ID.
    pub case_id: SignoffCaseId,
    /// Canonical digest of the complete case definition.
    pub case_digest: Digest,
    /// Complete catalog identity.
    pub catalog_digest: Digest,
    /// Canonical exact-target manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Exact retained OCI payload identity.
    pub artifact_payload_digest: Digest,
    /// Validated engine identity.
    pub engine_identity_digest: Digest,
    /// Installed Rust baseline identity.
    pub baseline_digest: Digest,
    /// Canonical binding identity used to derive all attempt namespaces.
    pub execution_binding_digest: Digest,
}

/// Distinct mutable namespaces assigned to one isolated case attempt.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CaseNamespaces {
    /// Private workspace identity.
    pub workspace_digest: Digest,
    /// Private process-environment identity.
    pub environment_digest: Digest,
    /// Private cache namespace identity.
    pub cache_namespace_digest: Digest,
    /// Private engine-session identity.
    pub session_namespace_digest: Digest,
}

/// The three infrastructure failures eligible for a declared fresh attempt.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ExecutionInfrastructureFailureClass {
    /// Shared orchestration transport disappeared during the isolated attempt.
    OrchestrationTransport,
    /// An immutable remote could not be fetched during the isolated attempt.
    ImmutableRemoteFetch,
    /// The runner could not provide capacity for the isolated attempt.
    RunnerCapacity,
}

impl ExecutionInfrastructureFailureClass {
    fn catalog_class(self) -> InfrastructureFailureClass {
        match self {
            Self::OrchestrationTransport => InfrastructureFailureClass::OrchestrationTransportLost,
            Self::ImmutableRemoteFetch => InfrastructureFailureClass::ImmutableRemoteUnavailable,
            Self::RunnerCapacity => InfrastructureFailureClass::WorkspaceMaterializationInterrupted,
        }
    }
}

/// Terminal result of one retained attempt.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "outcome", deny_unknown_fields)]
pub enum CaseAttemptOutcome {
    /// Every case-local assertion passed.
    Passed {
        /// Typed, bounded observation identity.
        observation_digest: Digest,
    },
    /// Subject behaviour violated a reviewed assertion and may never be retried.
    AssertionFailed {
        /// Safe diagnostic with stable semantic coordinates.
        diagnostic: ConformanceDiagnostic,
    },
    /// Infrastructure failed in one of the three closed retry classes.
    InfrastructureFailed {
        /// Closed infrastructure failure class.
        class: ExecutionInfrastructureFailureClass,
        /// Safe diagnostic with stable semantic coordinates.
        diagnostic: ConformanceDiagnostic,
    },
}

/// Shared-work counters observed from inside an isolated attempt.
#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AttemptSharedWorkCounters {
    /// Exact-target artifact constructions or imports.
    pub artifact_materializations: u32,
    /// Engine service starts.
    pub engine_starts: u32,
    /// Installed baseline materializations.
    pub baseline_materializations: u32,
}

impl AttemptSharedWorkCounters {
    fn is_zero(&self) -> bool {
        self.artifact_materializations == 0
            && self.engine_starts == 0
            && self.baseline_materializations == 0
    }
}

/// One ordered attempt retaining its immutable binding and isolated mutable state.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CaseAttempt {
    /// Contiguous one-based attempt number.
    pub attempt: NonZeroCount,
    /// Unchanged binding shared by every attempt of the case.
    pub execution_binding_digest: Digest,
    /// Deterministic isolated namespaces derived from binding and attempt number.
    pub namespaces: CaseNamespaces,
    /// Shared work observed within this attempt; every count must remain zero.
    pub shared_work: AttemptSharedWorkCounters,
    /// Positive bounded attempt duration.
    pub elapsed_millis: NonZeroMillis,
    /// Complete terminal attempt outcome.
    pub outcome: CaseAttemptOutcome,
}

/// Complete ordered observation for one catalog case.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SignoffCaseObservation {
    /// Stable case ID.
    pub case_id: SignoffCaseId,
    /// Immutable execution binding.
    pub execution_binding_digest: Digest,
    /// Every attempt in one-based order, including failures preceding a success.
    pub attempts: Vec<CaseAttempt>,
    /// Final result, equal to the last retained attempt rather than a rewritten summary.
    pub final_outcome: CaseAttemptOutcome,
    /// Exact sum of retained attempt durations.
    pub elapsed_millis: NonZeroMillis,
}

/// Admitted case history with no erased failure or duplicated shared work.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AdmittedCaseObservation {
    observation: SignoffCaseObservation,
    observation_digest: Digest,
}

impl AdmittedCaseObservation {
    /// Borrows the complete retained attempt history.
    pub fn observation(&self) -> &SignoffCaseObservation {
        &self.observation
    }

    /// Borrows the canonical case-observation identity.
    pub fn observation_digest(&self) -> &Digest {
        &self.observation_digest
    }
}

/// Lifecycle and isolation counters collected around one bounded case fan-out.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FanoutCounters {
    /// Exact target service starts.
    pub engine_starts: u32,
    /// Installed Rust baseline materializations.
    pub baseline_materializations: u32,
    /// Additional SDK installs after baseline materialization.
    pub additional_installs: u32,
    /// Exact target service stops.
    pub engine_stops: u32,
    /// Exact target child-process reap observations.
    pub child_reaps: u32,
    /// Cross-case mutable-state accesses.
    pub cross_case_state_accesses: u32,
    /// Sibling cases abandoned after an independent failure.
    pub abandoned_siblings: u32,
    /// Artifact constructions or imports performed during fan-out.
    pub artifact_materializations: u32,
}

/// Complete one-service, one-baseline bounded fan-out observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FanoutObservation {
    /// Complete catalog identity.
    pub catalog_digest: Digest,
    /// Exact artifact manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Exact retained OCI payload identity.
    pub artifact_payload_digest: Digest,
    /// Validated engine identity.
    pub engine_identity_digest: Digest,
    /// Installed Rust baseline identity.
    pub baseline_digest: Digest,
    /// Configured positive concurrency bound.
    pub maximum_concurrency: NonZeroCount,
    /// Highest concurrently active case count observed.
    pub peak_concurrency: NonZeroCount,
    /// Stable catalog-indexed case results.
    pub cases: Vec<SignoffCaseObservation>,
    /// Actual completion order, retained separately from stable result order.
    pub completion_order: Vec<SignoffCaseId>,
    /// Lifecycle, duplication, and isolation counters.
    pub counters: FanoutCounters,
    /// Positive bounded aggregate fan-out duration.
    pub elapsed_millis: NonZeroMillis,
}

/// Case identity and all mutable namespaces presented to the fan-out topology validator.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FanoutCaseTopology {
    /// Stable case ID retained in result-index order.
    pub case_id: SignoffCaseId,
    /// One isolated namespace set for every retained attempt.
    pub attempts: Vec<CaseNamespaces>,
}

/// Engine-free projection used to validate scheduling, shared identities, and cleanup.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FanoutTopologyObservation {
    /// Complete catalog identity.
    pub catalog_digest: Digest,
    /// Exact artifact manifest identity.
    pub artifact_manifest_digest: Digest,
    /// Exact retained OCI payload identity.
    pub artifact_payload_digest: Digest,
    /// Validated engine identity.
    pub engine_identity_digest: Digest,
    /// Installed Rust baseline identity.
    pub baseline_digest: Digest,
    /// Configured positive concurrency bound.
    pub maximum_concurrency: NonZeroCount,
    /// Highest concurrently active case count observed.
    pub peak_concurrency: NonZeroCount,
    /// Stable result-index order and every private attempt namespace.
    pub cases: Vec<FanoutCaseTopology>,
    /// Actual completion order, independent of result indexing.
    pub completion_order: Vec<SignoffCaseId>,
    /// Lifecycle, duplication, and isolation counters.
    pub counters: FanoutCounters,
}

/// Admitted fan-out whose cases retain all attempts in stable catalog order.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AdmittedFanout {
    cases: Vec<AdmittedCaseObservation>,
    fanout_digest: Digest,
}

impl AdmittedFanout {
    /// Borrows admitted cases in canonical catalog order.
    pub fn cases(&self) -> &[AdmittedCaseObservation] {
        &self.cases
    }

    /// Borrows the canonical identity of the complete fan-out.
    pub fn fanout_digest(&self) -> &Digest {
        &self.fanout_digest
    }
}

/// Binds one catalog case to the exact artifact, engine, and installed baseline.
pub fn bind_case_execution(
    catalog: &CaseCatalog,
    case_id: &SignoffCaseId,
    artifact: &AdmittedArtifact,
    baseline: &InstalledRustBaseline,
) -> Result<CaseExecutionBinding, ConformanceDiagnosticSet> {
    let Some(case) = catalog.cases().get(case_id) else {
        return Err(execution_error(
            ConformanceDiagnosticCode::SignoffCaseUnknown,
            Some(case_id.clone()),
            "case is absent from the admitted catalog",
        ));
    };
    let manifest = artifact.bundle().manifest();
    let subject_matches = match catalog.subject() {
        SubjectIdentity::Revision(revision) => revision == &manifest.subject_revision,
        SubjectIdentity::SourceDigest(digest) => digest == &manifest.subject_source_digest,
    };
    if !baseline_is_self_consistent(baseline)
        || catalog.target_digest() != &manifest.target_descriptor_digest
        || catalog.platform() != &manifest.platform
        || !subject_matches
        || baseline.artifact_manifest_digest != *artifact.manifest_digest()
        || baseline.artifact_payload_digest != *artifact.payload_digest()
    {
        return Err(execution_error(
            ConformanceDiagnosticCode::SignoffEngineIdentityMismatch,
            Some(case_id.clone()),
            "case catalog artifact engine and baseline identities do not match",
        ));
    }
    let case_digest = canonical_digest(DigestDomain::ConformanceCaseExecution, case)
        .map_err(|_| encoding_error(Some(case_id.clone())))?;
    let mut binding = CaseExecutionBinding {
        case_id: case_id.clone(),
        case_digest,
        catalog_digest: catalog.digest().clone(),
        artifact_manifest_digest: artifact.manifest_digest().clone(),
        artifact_payload_digest: artifact.payload_digest().clone(),
        engine_identity_digest: baseline.engine.identity_digest.clone(),
        baseline_digest: baseline.baseline_digest.clone(),
        execution_binding_digest: Digest::sha256([]),
    };
    binding.execution_binding_digest = canonical_digest(
        DigestDomain::ConformanceCaseExecution,
        &(
            &binding.case_id,
            &binding.case_digest,
            &binding.catalog_digest,
            &binding.artifact_manifest_digest,
            &binding.artifact_payload_digest,
            &binding.engine_identity_digest,
            &binding.baseline_digest,
        ),
    )
    .map_err(|_| encoding_error(Some(case_id.clone())))?;
    Ok(binding)
}

/// Derives all private mutable namespaces from an immutable binding and attempt number.
pub fn derive_case_namespaces(
    binding: &CaseExecutionBinding,
    attempt: NonZeroCount,
) -> Result<CaseNamespaces, ConformanceDiagnosticSet> {
    let derive = |kind: &'static str| {
        canonical_digest(
            DigestDomain::ConformanceCaseExecution,
            &(&binding.execution_binding_digest, attempt, kind),
        )
        .map_err(|_| encoding_error(Some(binding.case_id.clone())))
    };
    Ok(CaseNamespaces {
        workspace_digest: derive("workspace")?,
        environment_digest: derive("environment")?,
        cache_namespace_digest: derive("cache")?,
        session_namespace_digest: derive("session")?,
    })
}

/// Decodes a case observation only when it already uses canonical JSON bytes.
pub fn decode_case_observation(bytes: &[u8]) -> Result<SignoffCaseObservation, CanonicalError> {
    decode_canonical(bytes)
}

/// Admits a contiguous retry history without erasing failures or rebuilding shared work.
pub fn admit_case_observation(
    case: &CaseDefinition,
    binding: &CaseExecutionBinding,
    observation: SignoffCaseObservation,
) -> Result<AdmittedCaseObservation, ConformanceDiagnosticSet> {
    validate_case_observation(case, binding, &observation)?;
    let observation_digest = canonical_digest(DigestDomain::ConformanceCaseExecution, &observation)
        .map_err(|_| encoding_error(Some(case.id.clone())))?;
    Ok(AdmittedCaseObservation {
        observation,
        observation_digest,
    })
}

/// Validates a complete retry history without computing a standalone evidence identity.
///
/// Atomic verdict derivation hashes the complete normalized observation tree once, so it uses
/// this borrowed form to avoid redundantly serializing hundreds of already-bound case records.
pub fn validate_case_observation(
    case: &CaseDefinition,
    binding: &CaseExecutionBinding,
    observation: &SignoffCaseObservation,
) -> Result<(), ConformanceDiagnosticSet> {
    if !binding_matches_case(case, binding)
        || observation.case_id != case.id
        || binding.case_id != case.id
        || observation.execution_binding_digest != binding.execution_binding_digest
        || observation.attempts.is_empty()
        || observation.attempts.len() as u32 > case.retry.maximum_attempts.get()
    {
        return Err(retry_error(&case.id));
    }
    let mut elapsed = 0_u64;
    for (index, attempt) in observation.attempts.iter().enumerate() {
        let expected_number = u32::try_from(index + 1).unwrap_or(u32::MAX);
        let expected_namespaces = derive_case_namespaces(
            binding,
            NonZeroCount::new(expected_number).map_err(|_| retry_error(&case.id))?,
        )?;
        if attempt.attempt.get() != expected_number
            || attempt.execution_binding_digest != binding.execution_binding_digest
            || attempt.namespaces != expected_namespaces
            || !attempt.shared_work.is_zero()
        {
            return Err(retry_error(&case.id));
        }
        elapsed = elapsed
            .checked_add(attempt.elapsed_millis.get())
            .ok_or_else(|| retry_error(&case.id))?;
        if index + 1 < observation.attempts.len() {
            let retryable = match attempt.outcome {
                CaseAttemptOutcome::InfrastructureFailed { class, .. } => {
                    case.retry.retryable.contains(&class.catalog_class())
                }
                CaseAttemptOutcome::Passed { .. } | CaseAttemptOutcome::AssertionFailed { .. } => {
                    false
                }
            };
            if !retryable {
                return Err(retry_error(&case.id));
            }
        }
    }
    if observation
        .attempts
        .last()
        .is_none_or(|attempt| attempt.outcome != observation.final_outcome)
        || elapsed != observation.elapsed_millis.get()
    {
        return Err(retry_error(&case.id));
    }
    Ok(())
}

/// Admits the complete catalog fan-out only when lifecycle and namespace isolation are exact.
pub fn admit_case_fanout(
    catalog: &CaseCatalog,
    artifact: &AdmittedArtifact,
    baseline: &InstalledRustBaseline,
    observation: FanoutObservation,
) -> Result<AdmittedFanout, ConformanceDiagnosticSet> {
    let ordered_ids = catalog.cases().keys().cloned().collect::<Vec<_>>();
    let topology = FanoutTopologyObservation {
        catalog_digest: observation.catalog_digest.clone(),
        artifact_manifest_digest: observation.artifact_manifest_digest.clone(),
        artifact_payload_digest: observation.artifact_payload_digest.clone(),
        engine_identity_digest: observation.engine_identity_digest.clone(),
        baseline_digest: observation.baseline_digest.clone(),
        maximum_concurrency: observation.maximum_concurrency,
        peak_concurrency: observation.peak_concurrency,
        cases: observation
            .cases
            .iter()
            .map(|case| FanoutCaseTopology {
                case_id: case.case_id.clone(),
                attempts: case
                    .attempts
                    .iter()
                    .map(|attempt| attempt.namespaces.clone())
                    .collect(),
            })
            .collect(),
        completion_order: observation.completion_order.clone(),
        counters: observation.counters.clone(),
    };
    admit_fanout_topology(
        &ordered_ids,
        catalog.digest(),
        artifact.manifest_digest(),
        artifact.payload_digest(),
        &baseline.engine.identity_digest,
        &baseline.baseline_digest,
        &topology,
    )?;

    let mut admitted = Vec::with_capacity(observation.cases.len());
    for case_observation in observation.cases.iter().cloned() {
        let case = catalog
            .cases()
            .get(&case_observation.case_id)
            .ok_or_else(fanout_error)?;
        let binding = bind_case_execution(catalog, &case.id, artifact, baseline)?;
        let result = admit_case_observation(case, &binding, case_observation)?;
        admitted.push(result);
    }
    let fanout_digest = canonical_digest(DigestDomain::ConformanceCaseExecution, &observation)
        .map_err(|_| encoding_error(None))?;
    Ok(AdmittedFanout {
        cases: admitted,
        fanout_digest,
    })
}

/// Validates bounded scheduling and isolation without constructing an engine or case program.
pub fn admit_fanout_topology(
    expected_case_ids: &[SignoffCaseId],
    expected_catalog_digest: &Digest,
    expected_artifact_manifest_digest: &Digest,
    expected_artifact_payload_digest: &Digest,
    expected_engine_identity_digest: &Digest,
    expected_baseline_digest: &Digest,
    observation: &FanoutTopologyObservation,
) -> Result<Digest, ConformanceDiagnosticSet> {
    let observed_ids = observation
        .cases
        .iter()
        .map(|case| case.case_id.clone())
        .collect::<Vec<_>>();
    let completion = observation
        .completion_order
        .iter()
        .cloned()
        .collect::<BTreeSet<_>>();
    let lifecycle_exact = observation.counters.engine_starts == 1
        && observation.counters.baseline_materializations == 1
        && observation.counters.additional_installs == 0
        && observation.counters.engine_stops == 1
        && observation.counters.child_reaps == 1
        && observation.counters.cross_case_state_accesses == 0
        && observation.counters.abandoned_siblings == 0
        && observation.counters.artifact_materializations == 0;
    if expected_case_ids.is_empty()
        || observation.catalog_digest != *expected_catalog_digest
        || observation.artifact_manifest_digest != *expected_artifact_manifest_digest
        || observation.artifact_payload_digest != *expected_artifact_payload_digest
        || observation.engine_identity_digest != *expected_engine_identity_digest
        || observation.baseline_digest != *expected_baseline_digest
        || observed_ids != expected_case_ids
        || completion.len() != expected_case_ids.len()
        || completion != expected_case_ids.iter().cloned().collect()
        || observation.peak_concurrency.get() > observation.maximum_concurrency.get()
        || observation.maximum_concurrency.get() as usize > expected_case_ids.len()
        || !lifecycle_exact
    {
        return Err(fanout_error());
    }
    let mut namespaces = BTreeSet::new();
    for case in &observation.cases {
        if case.attempts.is_empty() {
            return Err(fanout_error());
        }
        for attempt in &case.attempts {
            for digest in [
                &attempt.workspace_digest,
                &attempt.environment_digest,
                &attempt.cache_namespace_digest,
                &attempt.session_namespace_digest,
            ] {
                if !namespaces.insert(digest.clone()) {
                    return Err(fanout_error());
                }
            }
        }
    }
    canonical_digest(DigestDomain::ConformanceCaseExecution, observation)
        .map_err(|_| encoding_error(None))
}

fn exact_engine_digest(engine: &ExactEngineIdentity) -> Result<Digest, ConformanceDiagnosticSet> {
    canonical_digest(
        DigestDomain::ConformanceInstalledBaseline,
        &(
            &engine.target_descriptor_digest,
            &engine.target_revision,
            &engine.engine_version,
            &engine.platform,
            &engine.rust_manifest_digest,
            &engine.rust_descriptor_digest,
        ),
    )
    .map_err(|_| encoding_error(None))
}

fn baseline_digest(baseline: &InstalledRustBaseline) -> Result<Digest, ConformanceDiagnosticSet> {
    canonical_digest(
        DigestDomain::ConformanceInstalledBaseline,
        &(
            &baseline.artifact_digest,
            &baseline.artifact_manifest_digest,
            &baseline.artifact_payload_digest,
            &baseline.engine,
            &baseline.cli_digest,
            &baseline.installed_config_digest,
            &baseline.dependency_descriptor_digest,
            &baseline.runner_image_digest,
        ),
    )
    .map_err(|_| encoding_error(None))
}

fn baseline_is_self_consistent(baseline: &InstalledRustBaseline) -> bool {
    exact_engine_digest(&baseline.engine)
        .is_ok_and(|digest| digest == baseline.engine.identity_digest)
        && baseline_digest(baseline).is_ok_and(|digest| digest == baseline.baseline_digest)
}

fn binding_matches_case(case: &CaseDefinition, binding: &CaseExecutionBinding) -> bool {
    let case_digest = canonical_digest(DigestDomain::ConformanceCaseExecution, case);
    let binding_digest = canonical_digest(
        DigestDomain::ConformanceCaseExecution,
        &(
            &binding.case_id,
            &binding.case_digest,
            &binding.catalog_digest,
            &binding.artifact_manifest_digest,
            &binding.artifact_payload_digest,
            &binding.engine_identity_digest,
            &binding.baseline_digest,
        ),
    );
    case_digest.is_ok_and(|digest| digest == binding.case_digest)
        && binding_digest.is_ok_and(|digest| digest == binding.execution_binding_digest)
}

fn execution_error(
    code: ConformanceDiagnosticCode,
    case_id: Option<SignoffCaseId>,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Case),
            case_id,
            ..DiagnosticCoordinate::default()
        },
        detail,
    )])
    .expect("one execution diagnostic is non-empty")
}

fn encoding_error(case_id: Option<SignoffCaseId>) -> ConformanceDiagnosticSet {
    execution_error(
        ConformanceDiagnosticCode::SignoffCaseFailed,
        case_id,
        "sign-off execution observation cannot be encoded canonically",
    )
}

fn retry_error(case_id: &SignoffCaseId) -> ConformanceDiagnosticSet {
    execution_error(
        ConformanceDiagnosticCode::SignoffRetryInvalid,
        Some(case_id.clone()),
        "attempt history is non-contiguous retrying assertions or rebuilding shared work",
    )
}

fn fanout_error() -> ConformanceDiagnosticSet {
    execution_error(
        ConformanceDiagnosticCode::SignoffCaseIsolationViolation,
        None,
        "fan-out lifecycle stable ordering or mutable namespace isolation is invalid",
    )
}
