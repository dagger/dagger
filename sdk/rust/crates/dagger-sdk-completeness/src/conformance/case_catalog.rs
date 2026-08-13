//! Closed fixture registry and exact sign-off case catalog.
//!
//! Catalog data selects only typed programs. Runtime paths, command text, package selectors, and
//! mutable references are structurally absent, while total forward and reverse indexes prevent an
//! assertion or capability from being silently dropped or widened.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::client_generation::ClientSignoffCase;
use crate::model::{CanonicalSet, CapabilityId, CommitSha, Digest, TargetDigest};
use crate::module_authoring::ModuleSignoffCase;

use super::{
    AssertionCatalog, AssertionFamily, AssertionId, AssertionOrigin, ConformanceDiagnostic,
    ConformanceDiagnosticCode, ConformanceDiagnosticSet, ConformanceFormatVersion,
    ConformanceScope, DiagnosticCoordinate, DiagnosticPhase, FixtureContextId, FixtureExecutorId,
    NetworkPolicyId, NonZeroCount, NonZeroMillis, PlatformDescriptor, ReviewedFixtureId,
    SignoffCaseId,
};

/// Exact immutable implementation identity used to bind the case catalog.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind", content = "identity")]
pub enum SubjectIdentity {
    /// Exact repository revision when the complete subject is committed.
    Revision(CommitSha),
    /// Canonical source-content identity for an uncommitted or assembled subject.
    SourceDigest(Digest),
}

/// All pinned subject-conformance checks in the common SDK harness.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CommonHarnessCheck {
    /// Dependency enumeration succeeds for the installed SDK.
    DepsListSucceeds,
    /// Engine-required mode reports the exact target version.
    EngineRequiredReportsVersion,
    /// Generation exposes the SDK generator contract.
    GenerateExposesGenerator,
    /// Generation respects the selected working directory boundary.
    GenerateRespectsCwd,
    /// Generation completes for a valid initialized module.
    GenerateSucceeds,
    /// Initialization preserves files outside SDK ownership.
    InitModuleDoesNotRemoveExistingFiles,
    /// Module initialization does not claim module-configuration ownership.
    InitModuleDoesNotWriteConfig,
    /// Module initialization honors the requested source root.
    InitModuleHonorsCustomPath,
    /// Module initialization creates its required seed content.
    InitModuleSeedsFiles,
    /// Initialization records the selected authoring SDK.
    InitRecordsAuthoringSdk,
    /// Initialization registers the module in workspace metadata.
    InitRegistersModule,
    /// Initialization creates the required module source scaffold.
    InitScaffoldsModule,
    /// Initialization writes only its owned module metadata.
    InitWritesModuleConfig,
    /// Installation marks the dependency as an SDK.
    InstallMarksAsSdk,
    /// Installation registers the SDK dependency.
    InstallRegistersSdk,
    /// The generated module can be loaded by the common lifecycle.
    ScaffoldedModuleLoads,
    /// The SDK reports its supported module options.
    SdkReportsModuleOptions,
}

/// Representative generated Core API shapes required by exact sign-off.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CoreCaseShape {
    /// Generated scalar encoding and decoding.
    Scalar,
    /// Generated closed enum representation.
    Enum,
    /// Generated input-object construction.
    Input,
    /// Generated object selection and decoding.
    Object,
    /// Generated interface representation and concrete values.
    Interface,
    /// Nullable selection and decoding.
    Nullable,
    /// List-of-object selection and element typing.
    ListObject,
    /// Expected-type propagation through generated selections.
    ExpectedType,
    /// Schema `Void` mapping and unit-result behaviour.
    Void,
}

/// Exact engine-integration cases migrated into umbrella sign-off.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum EngineIntegrationCase {
    /// Built-in SDK reference resolution.
    Resolution,
    /// Initialization into an empty workspace.
    InitEmpty,
    /// Adoption of an existing Cargo project.
    InitExisting,
    /// Initialization when generation is deliberately disabled.
    InitNoGenerate,
    /// Typed engine operation compilation.
    Operations,
    /// Runtime construction from checked generated content.
    RuntimeChecked,
    /// Isolated compatibility with the legacy runtime path.
    RuntimeLegacy,
    /// Rejection of generated lockfile or toolchain ownership drift.
    NegativeGeneratedLockToolchain,
    /// Rejection of writes outside SDK-owned paths.
    NegativePathOwnership,
    /// Redaction of credentials and sensitive operational values.
    NegativeRedaction,
}

/// Definitive Go-client observations translated to idiomatic public Rust calls.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum GoClientBehaviour {
    /// Directory query construction and result observation.
    Directory,
    /// Git query construction including explicit option values.
    Git,
    /// Container query construction and result observation.
    Container,
    /// Container mutation visibility across chained selections.
    ContainerMutation,
    /// List result shape and element ordering.
    List,
    /// Execution failure maps to the public typed Rust error.
    TypedExecError,
    /// Execution error output fields remain available.
    ExecErrorOutputFields,
    /// Empty execution output remains an explicit value.
    ExecErrorEmptyOutput,
    /// Non-execution failures remain outside the execution-error variant.
    NonExecErrorSeparation,
}

/// Eight closed program families; there is no arbitrary-command escape hatch.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CaseFamily {
    /// Portable checks owned by the shared SDK harness.
    CommonHarness,
    /// Default connector lifecycle and transport.
    StableConnector,
    /// Generated Core API surface.
    CoreGeneratedApi,
    /// Engine lifecycle integration owned by the Rust SDK.
    EngineIntegration,
    /// Rust-authored module execution.
    ModuleAuthoring,
    /// Standalone generated-client lifecycle.
    StandaloneClient,
    /// Selected definitive client observations.
    DefinitiveGoClient,
    /// Remaining authority-selected integration assertions.
    IntegrationAssertion,
}

impl From<CaseFamily> for AssertionFamily {
    fn from(value: CaseFamily) -> Self {
        match value {
            CaseFamily::CommonHarness => Self::CommonHarness,
            CaseFamily::StableConnector => Self::StableConnector,
            CaseFamily::CoreGeneratedApi => Self::CoreGeneratedApi,
            CaseFamily::EngineIntegration => Self::EngineIntegration,
            CaseFamily::ModuleAuthoring => Self::ModuleAuthoring,
            CaseFamily::StandaloneClient => Self::StandaloneClient,
            CaseFamily::DefinitiveGoClient => Self::DefinitiveGoClient,
            CaseFamily::IntegrationAssertion => Self::IntegrationAssertion,
        }
    }
}

/// One typed production executor route.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "program")]
pub enum CaseProgram {
    /// Execute one pinned portable subject check.
    CommonHarness {
        /// Exact subject-conformance check; harness-self checks have no variant.
        check: CommonHarnessCheck,
    },
    /// Exercise connector startup, transport, and bounded shutdown.
    StableConnector,
    /// Exercise one representative generated Core shape.
    CoreShape {
        /// Exact generated shape selected by the case.
        shape: CoreCaseShape,
    },
    /// Exercise one engine-integration scenario.
    EngineIntegration {
        /// Exact closed integration scenario.
        case: EngineIntegrationCase,
    },
    /// Exercise one Rust module-authoring scenario.
    ModuleAuthoring {
        /// Exact closed module scenario imported from the child inventory.
        case: ModuleSignoffCase,
    },
    /// Exercise one standalone-client scenario.
    StandaloneClient {
        /// Exact closed client scenario imported from the child inventory.
        case: ClientSignoffCase,
    },
    /// Exercise one definitive client observation through public Rust APIs.
    DefinitiveGoClient {
        /// Exact observable behaviour, independent of Go implementation structure.
        behaviour: GoClientBehaviour,
    },
    /// Exercise one reviewed integration assertion through its self-bound fixture.
    IntegrationAssertion {
        /// Registry identity which prevents runtime fixture or path substitution.
        fixture: ReviewedFixtureId,
    },
}

impl CaseProgram {
    /// Returns the sole family permitted to execute this program.
    pub const fn family(&self) -> CaseFamily {
        match self {
            Self::CommonHarness { .. } => CaseFamily::CommonHarness,
            Self::StableConnector => CaseFamily::StableConnector,
            Self::CoreShape { .. } => CaseFamily::CoreGeneratedApi,
            Self::EngineIntegration { .. } => CaseFamily::EngineIntegration,
            Self::ModuleAuthoring { .. } => CaseFamily::ModuleAuthoring,
            Self::StandaloneClient { .. } => CaseFamily::StandaloneClient,
            Self::DefinitiveGoClient { .. } => CaseFamily::DefinitiveGoClient,
            Self::IntegrationAssertion { .. } => CaseFamily::IntegrationAssertion,
        }
    }
}

/// Typed infrastructure failures which may permit a fresh isolated attempt.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum InfrastructureFailureClass {
    /// The shared orchestration transport disappeared before an observation completed.
    OrchestrationTransportLost,
    /// A pinned immutable remote could not be fetched.
    ImmutableRemoteUnavailable,
    /// An isolated workspace could not be materialized completely.
    WorkspaceMaterializationInterrupted,
}

/// Bounded retry policy. Assertion failures never appear in the retryable vocabulary.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RetryPolicy {
    /// Total attempts including the initial execution.
    pub maximum_attempts: NonZeroCount,
    /// Infrastructure-only failures eligible for a fresh isolated attempt.
    pub retryable: CanonicalSet<InfrastructureFailureClass>,
}

/// Scheduling class used without exposing execution commands in the catalog.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ConcurrencyClass {
    /// Case reads immutable shared state and may run concurrently.
    SharedReadOnly,
    /// Case requires a private workspace but no process-wide mutation lock.
    IsolatedWorkspace,
    /// Case mutates shared installation state and must run exclusively.
    ExclusiveMutation,
}

/// One stable fixture and its exact closed executor binding.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewedFixture {
    /// Stable registry identity referenced by assertions and cases.
    pub id: ReviewedFixtureId,
    /// Semantic context which must equal the assertion merge key.
    pub context_id: FixtureContextId,
    /// Closed production executor; it is never a command or path.
    pub executor: FixtureExecutorId,
    /// Typed program selected by the executor.
    pub program: CaseProgram,
    /// Complete immutable inputs which invalidate cached fixture materialization.
    pub immutable_inputs: CanonicalSet<Digest>,
    /// Reviewed network boundary for this fixture.
    pub network: NetworkPolicyId,
    /// Sole family permitted to invoke the fixture.
    pub permitted_family: CaseFamily,
    /// Digest over every field which affects fixture behaviour.
    pub fixture_digest: Digest,
}

/// Authored fixture registry input retained for review.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FixtureRegistryInput {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target shared by every fixture.
    pub target_digest: TargetDigest,
    /// Authored fixtures retained as a list so duplicate IDs remain observable.
    pub fixtures: Vec<ReviewedFixture>,
}

/// Validated fixture registry with canonical private identity lookup.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FixtureRegistry {
    target_digest: TargetDigest,
    fixtures: BTreeMap<ReviewedFixtureId, ReviewedFixture>,
    digest: Digest,
}

impl FixtureRegistry {
    /// Borrows fixtures in stable identity order.
    pub fn fixtures(&self) -> &BTreeMap<ReviewedFixtureId, ReviewedFixture> {
        &self.fixtures
    }

    /// Returns the complete domain-separated registry identity.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }
}

/// One complete sign-off case. Executable text is absent by construction.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CaseDefinition {
    /// Stable case identity used by observations and retries.
    pub id: SignoffCaseId,
    /// Closed execution family.
    pub family: CaseFamily,
    /// Typed executor program with no arbitrary-command escape hatch.
    pub program: CaseProgram,
    /// Reviewed fixture registry identity.
    pub fixture_id: ReviewedFixtureId,
    /// Immutable fixture identity copied into the case verdict boundary.
    pub fixture_digest: Digest,
    /// Complete assertion set observed by this case.
    pub assertion_ids: CanonicalSet<AssertionId>,
    /// Exact union of the case assertions' capability claims.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Per-attempt execution bound.
    pub timeout: NonZeroMillis,
    /// Infrastructure-only bounded retry policy.
    pub retry: RetryPolicy,
    /// Reviewed network boundary, equal to the fixture policy.
    pub network: NetworkPolicyId,
    /// Scheduling isolation required by the program.
    pub concurrency_class: ConcurrencyClass,
}

/// Complete authored case-catalog input.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CaseCatalogInput {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target under test.
    pub target_digest: TargetDigest,
    /// Immutable Rust implementation identity.
    pub subject: SubjectIdentity,
    /// Platform on which these cases are valid.
    pub platform: PlatformDescriptor,
    /// Reviewed applicability scope identity.
    pub scope_digest: Digest,
    /// Compiled assertion identity which the case graph must consume exactly.
    pub assertion_catalog_digest: Digest,
    /// Compiled fixture identity which prevents runtime substitution.
    pub fixture_registry_digest: Digest,
    /// Authored cases retained as a list so duplicates remain observable.
    pub cases: Vec<CaseDefinition>,
}

/// Admitted case catalog and total reverse indexes.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CaseCatalog {
    target_digest: TargetDigest,
    subject: SubjectIdentity,
    platform: PlatformDescriptor,
    cases: BTreeMap<SignoffCaseId, CaseDefinition>,
    assertion_cases: BTreeMap<AssertionId, CanonicalSet<SignoffCaseId>>,
    capability_cases: BTreeMap<CapabilityId, CanonicalSet<SignoffCaseId>>,
    digest: Digest,
}

impl CaseCatalog {
    /// Borrows the exact target shared by every case.
    pub fn target_digest(&self) -> &TargetDigest {
        &self.target_digest
    }

    /// Borrows the immutable Rust implementation identity.
    pub fn subject(&self) -> &SubjectIdentity {
        &self.subject
    }

    /// Borrows the platform on which the catalog may execute.
    pub fn platform(&self) -> &PlatformDescriptor {
        &self.platform
    }

    /// Borrows cases in canonical identity order.
    pub fn cases(&self) -> &BTreeMap<SignoffCaseId, CaseDefinition> {
        &self.cases
    }

    /// Borrows the total assertion-to-case reverse index.
    pub fn assertion_cases(&self) -> &BTreeMap<AssertionId, CanonicalSet<SignoffCaseId>> {
        &self.assertion_cases
    }

    /// Borrows the total capability-to-case reverse index.
    pub fn capability_cases(&self) -> &BTreeMap<CapabilityId, CanonicalSet<SignoffCaseId>> {
        &self.capability_cases
    }

    /// Returns the complete target, subject, platform, policy, and route identity.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }
}

/// Computes the reviewed digest a fixture must carry.
pub fn reviewed_fixture_digest(
    fixture: &ReviewedFixture,
) -> Result<Digest, ConformanceDiagnosticSet> {
    canonical_digest(
        DigestDomain::ConformanceFixtureRegistry,
        &(
            &fixture.id,
            &fixture.context_id,
            &fixture.executor,
            &fixture.program,
            &fixture.immutable_inputs,
            &fixture.network,
            fixture.permitted_family,
        ),
    )
    .map_err(|_| one_case_diagnostic(None, "fixture cannot be encoded canonically"))
}

/// Validates immutable fixture identities and their exact closed executor routes.
pub fn compile_fixture_registry(
    input: FixtureRegistryInput,
) -> Result<FixtureRegistry, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    let mut fixtures = BTreeMap::new();
    for fixture in input.fixtures {
        let expected_executor = fixture_executor_for(&fixture.program);
        let expected_digest = reviewed_fixture_digest(&fixture)?;
        let integration_self_bound = match &fixture.program {
            CaseProgram::IntegrationAssertion { fixture: selected } => selected == &fixture.id,
            _ => true,
        };
        if fixture.program.family() != fixture.permitted_family
            || fixture.executor != expected_executor
            || fixture.fixture_digest != expected_digest
            || fixture.immutable_inputs.is_empty()
            || !integration_self_bound
            || !known_network_policy(&fixture.network)
        {
            diagnostics.push(case_diagnostic(
                None,
                ConformanceDiagnosticCode::ConformanceCaseForbidden,
                "fixture executor program network or immutable identity is invalid",
            ));
        }
        if fixtures.insert(fixture.id.clone(), fixture).is_some() {
            diagnostics.push(case_diagnostic(
                None,
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "fixture identity is duplicated",
            ));
        }
    }
    if fixtures.is_empty() {
        diagnostics.push(case_diagnostic(
            None,
            ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
            "fixture registry is empty",
        ));
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }
    let digest = canonical_digest(
        DigestDomain::ConformanceFixtureRegistry,
        &(&input.target_digest, &fixtures),
    )
    .map_err(|_| one_case_diagnostic(None, "fixture registry cannot be encoded canonically"))?;
    Ok(FixtureRegistry {
        target_digest: input.target_digest,
        fixtures,
        digest,
    })
}

/// Compiles the complete catalog and rejects any incomplete or overbroad graph as one set.
pub fn compile_case_catalog(
    scope: &ConformanceScope,
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    input: CaseCatalogInput,
) -> Result<CaseCatalog, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    if input.target_digest != *scope.target_digest()
        || input.target_digest != fixtures.target_digest
        || input.scope_digest != *scope.digest()
        || input.assertion_catalog_digest != *assertions.digest()
        || input.fixture_registry_digest != *fixtures.digest()
    {
        diagnostics.push(case_diagnostic(
            None,
            ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
            "case catalog target scope assertion or fixture identity is stale",
        ));
    }

    let mut cases = BTreeMap::new();
    for case in input.cases {
        validate_case(&case, assertions, fixtures, &mut diagnostics);
        if cases.insert(case.id.clone(), case).is_some() {
            diagnostics.push(case_diagnostic(
                None,
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "case identity is duplicated",
            ));
        }
    }
    validate_fixed_programs(&cases, &mut diagnostics);
    validate_applicability_routes(scope, assertions, &cases, &mut diagnostics);

    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }
    let assertion_cases = reverse_index(cases.values().flat_map(|case| {
        case.assertion_ids
            .iter()
            .cloned()
            .map(|assertion_id| (assertion_id, case.id.clone()))
    }));
    let capability_cases = reverse_index(cases.values().flat_map(|case| {
        case.capability_ids
            .iter()
            .cloned()
            .map(|capability_id| (capability_id, case.id.clone()))
    }));
    let digest = canonical_digest(
        DigestDomain::ConformanceCaseCatalog,
        &(
            &input.target_digest,
            &input.subject,
            &input.platform,
            &input.scope_digest,
            &input.assertion_catalog_digest,
            &input.fixture_registry_digest,
            &cases,
            &assertion_cases,
            &capability_cases,
        ),
    )
    .map_err(|_| one_case_diagnostic(None, "case catalog cannot be encoded canonically"))?;
    Ok(CaseCatalog {
        target_digest: input.target_digest,
        subject: input.subject,
        platform: input.platform,
        cases,
        assertion_cases,
        capability_cases,
        digest,
    })
}

/// Returns all required fixed programs, excluding only per-authority integration assertions.
pub fn required_fixed_programs() -> BTreeSet<CaseProgram> {
    let mut programs = BTreeSet::new();
    programs.extend(
        required_common_harness_checks()
            .into_iter()
            .map(|check| CaseProgram::CommonHarness { check }),
    );
    programs.insert(CaseProgram::StableConnector);
    programs.extend(
        required_core_shapes()
            .into_iter()
            .map(|shape| CaseProgram::CoreShape { shape }),
    );
    programs.extend(
        required_engine_integration_cases()
            .into_iter()
            .map(|case| CaseProgram::EngineIntegration { case }),
    );
    programs.extend(
        required_module_authoring_cases()
            .into_iter()
            .map(|case| CaseProgram::ModuleAuthoring { case }),
    );
    programs.extend(
        required_standalone_client_cases()
            .into_iter()
            .map(|case| CaseProgram::StandaloneClient { case }),
    );
    programs.extend(
        required_go_client_behaviours()
            .into_iter()
            .map(|behaviour| CaseProgram::DefinitiveGoClient { behaviour }),
    );
    programs
}

/// Returns the exact 17 subject checks, deliberately excluding the harness-self check.
pub fn required_common_harness_checks() -> BTreeSet<CommonHarnessCheck> {
    use CommonHarnessCheck::*;
    BTreeSet::from([
        DepsListSucceeds,
        EngineRequiredReportsVersion,
        GenerateExposesGenerator,
        GenerateRespectsCwd,
        GenerateSucceeds,
        InitModuleDoesNotRemoveExistingFiles,
        InitModuleDoesNotWriteConfig,
        InitModuleHonorsCustomPath,
        InitModuleSeedsFiles,
        InitRecordsAuthoringSdk,
        InitRegistersModule,
        InitScaffoldsModule,
        InitWritesModuleConfig,
        InstallMarksAsSdk,
        InstallRegistersSdk,
        ScaffoldedModuleLoads,
        SdkReportsModuleOptions,
    ])
}

/// Returns every representative generated Core shape.
pub fn required_core_shapes() -> BTreeSet<CoreCaseShape> {
    use CoreCaseShape::*;
    BTreeSet::from([
        Scalar,
        Enum,
        Input,
        Object,
        Interface,
        Nullable,
        ListObject,
        ExpectedType,
        Void,
    ])
}

/// Returns the complete ten-case engine-integration inventory.
pub fn required_engine_integration_cases() -> BTreeSet<EngineIntegrationCase> {
    use EngineIntegrationCase::*;
    BTreeSet::from([
        Resolution,
        InitEmpty,
        InitExisting,
        InitNoGenerate,
        Operations,
        RuntimeChecked,
        RuntimeLegacy,
        NegativeGeneratedLockToolchain,
        NegativePathOwnership,
        NegativeRedaction,
    ])
}

/// Returns the exact module-authoring deferred inventory.
pub fn required_module_authoring_cases() -> BTreeSet<ModuleSignoffCase> {
    crate::required_module_signoff_cases()
}

/// Returns the exact standalone-client deferred inventory.
pub fn required_standalone_client_cases() -> BTreeSet<ClientSignoffCase> {
    crate::required_client_signoff_cases()
}

/// Returns all nine definitive client behaviours.
pub fn required_go_client_behaviours() -> BTreeSet<GoClientBehaviour> {
    use GoClientBehaviour::*;
    BTreeSet::from([
        Directory,
        Git,
        Container,
        ContainerMutation,
        List,
        TypedExecError,
        ExecErrorOutputFields,
        ExecErrorEmptyOutput,
        NonExecErrorSeparation,
    ])
}

fn validate_case(
    case: &CaseDefinition,
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    diagnostics: &mut Vec<ConformanceDiagnostic>,
) {
    let Some(fixture) = fixtures.fixtures.get(&case.fixture_id) else {
        diagnostics.push(case_diagnostic(
            Some(case.id.clone()),
            ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
            "case references an unknown fixture",
        ));
        return;
    };
    let expected_family = case.program.family();
    if case.family != expected_family
        || fixture.program != case.program
        || fixture.permitted_family != case.family
        || fixture.fixture_digest != case.fixture_digest
        || fixture.network != case.network
        || case.assertion_ids.is_empty()
        || case.capability_ids.is_empty()
        || !retry_policy_is_valid(&case.retry)
    {
        diagnostics.push(case_diagnostic(
            Some(case.id.clone()),
            ConformanceDiagnosticCode::ConformanceCaseForbidden,
            "case family fixture policy or bounded execution policy is invalid",
        ));
    }

    let mut expected_capabilities = Vec::new();
    for assertion_id in case.assertion_ids.iter() {
        let Some(assertion) = assertions.assertions().get(assertion_id) else {
            diagnostics.push(case_diagnostic(
                Some(case.id.clone()),
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "case references an unknown assertion",
            ));
            continue;
        };
        if assertion.fixture_context != fixture.context_id
            || !assertion
                .permitted_families
                .contains(&AssertionFamily::from(case.family))
        {
            diagnostics.push(case_diagnostic(
                Some(case.id.clone()),
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "case widens assertion fixture or family scope",
            ));
        }
        expected_capabilities.extend(assertion.capability_ids.iter().cloned());
    }
    if case.capability_ids != CanonicalSet::new(expected_capabilities) {
        diagnostics.push(case_diagnostic(
            Some(case.id.clone()),
            ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
            "case capability claims differ from its exact assertions",
        ));
    }
}

fn validate_fixed_programs(
    cases: &BTreeMap<SignoffCaseId, CaseDefinition>,
    diagnostics: &mut Vec<ConformanceDiagnostic>,
) {
    let expected = required_fixed_programs();
    let observed = cases
        .values()
        .filter(|case| case.family != CaseFamily::IntegrationAssertion)
        .map(|case| case.program.clone())
        .collect::<Vec<_>>();
    let observed_set = observed.iter().cloned().collect::<BTreeSet<_>>();
    if observed.len() != observed_set.len() || observed_set != expected {
        diagnostics.push(case_diagnostic(
            None,
            ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
            "fixed case program inventory is incomplete duplicated or unknown",
        ));
    }
}

fn validate_applicability_routes(
    scope: &ConformanceScope,
    assertions: &AssertionCatalog,
    cases: &BTreeMap<SignoffCaseId, CaseDefinition>,
    diagnostics: &mut Vec<ConformanceDiagnostic>,
) {
    for (case_id, expected_capabilities) in scope.case_capabilities() {
        let Some(case) = cases.get(case_id) else {
            diagnostics.push(case_diagnostic(
                Some(case_id.clone()),
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "applicable authority case route is missing",
            ));
            continue;
        };
        if &case.capability_ids != expected_capabilities {
            diagnostics.push(case_diagnostic(
                Some(case_id.clone()),
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "applicable authority case route widens its capability scope",
            ));
        }
    }
    for assertion in assertions.assertions().values().filter(|assertion| {
        assertion.origin == AssertionOrigin::Applicability
            && scope.assertion_capabilities().contains_key(&assertion.id)
            && scope.existing_records().values().any(|record| {
                record.assertion_ids.contains(&assertion.id) && !record.case_ids.is_empty()
            })
    }) {
        if !cases
            .values()
            .any(|case| case.assertion_ids.contains(&assertion.id))
        {
            diagnostics.push(case_diagnostic(
                None,
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "applicable assertion has no case route",
            ));
        }
    }
    for case in cases.values() {
        if case.family == CaseFamily::IntegrationAssertion
            && !scope.case_capabilities().contains_key(&case.id)
        {
            diagnostics.push(case_diagnostic(
                Some(case.id.clone()),
                ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
                "integration case is outside the reviewed applicability routes",
            ));
        }
    }
}

fn retry_policy_is_valid(policy: &RetryPolicy) -> bool {
    (policy.maximum_attempts.get() == 1 && policy.retryable.is_empty())
        || (policy.maximum_attempts.get() > 1 && !policy.retryable.is_empty())
}

/// Returns the exact production executor identity for one closed program.
pub fn fixture_executor_for(program: &CaseProgram) -> FixtureExecutorId {
    let value = match program.family() {
        CaseFamily::CommonHarness => "executor/common-harness",
        CaseFamily::StableConnector => "executor/stable-connector",
        CaseFamily::CoreGeneratedApi => "executor/core-generated-api",
        CaseFamily::EngineIntegration => "executor/engine-integration",
        CaseFamily::ModuleAuthoring => "executor/module-authoring",
        CaseFamily::StandaloneClient => "executor/standalone-client",
        CaseFamily::DefinitiveGoClient => "executor/definitive-go-client",
        CaseFamily::IntegrationAssertion => "executor/integration-assertion",
    };
    FixtureExecutorId::new(value).expect("closed executor identity is valid")
}

fn known_network_policy(policy: &NetworkPolicyId) -> bool {
    matches!(
        policy.as_str(),
        "network/engine-only" | "network/immutable-remote" | "network/manifest-and-engine"
    )
}

fn reverse_index<I, K>(edges: I) -> BTreeMap<K, CanonicalSet<SignoffCaseId>>
where
    I: IntoIterator<Item = (K, SignoffCaseId)>,
    K: Ord,
{
    let mut index = BTreeMap::<K, Vec<SignoffCaseId>>::new();
    for (key, case_id) in edges {
        index.entry(key).or_default().push(case_id);
    }
    index
        .into_iter()
        .map(|(key, cases)| (key, CanonicalSet::new(cases)))
        .collect()
}

fn case_diagnostic(
    case_id: Option<SignoffCaseId>,
    code: ConformanceDiagnosticCode,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Catalog),
            case_id,
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn one_case_diagnostic(
    case_id: Option<SignoffCaseId>,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([case_diagnostic(
        case_id,
        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
        detail,
    )])
    .expect("one case diagnostic is non-empty")
}
