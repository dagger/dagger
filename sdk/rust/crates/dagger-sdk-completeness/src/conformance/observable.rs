//! Rust-owned execution routes for observable authority contracts.
//!
//! Authority source code is evidence for an observable predicate, never an executable fixture.
//! This module joins the admitted assertion, fixture, and case catalogs into a closed registry
//! whose routes name only production Rust boundaries. The join is deliberately total: every
//! applicable integration case has one route, and a new authority group fails closed until its
//! Rust boundary is reviewed.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{CanonicalSet, CapabilityId, Digest, SourceLocator, TargetDigest};
use crate::{ClientSignoffCase, ModuleSignoffCase};

use super::{
    AssertionCatalog, AssertionFamily, AssertionId, AssertionOrigin, CaseCatalog, CaseFamily,
    CaseProgram, CommonHarnessCheck, ConformanceDiagnostic, ConformanceDiagnosticCode,
    ConformanceDiagnosticSet, ConformanceFormatVersion, CoreCaseShape, DiagnosticCoordinate,
    DiagnosticPhase, FixtureRegistry, ObservablePredicate, QueryObservation, ReviewedFixtureId,
    SignoffCaseId,
};

/// Production Rust boundary allowed to execute one reviewed observable fixture.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum RustObservableBoundary {
    /// The exact installed baseline exercises a bounded CLI lifecycle or workspace operation.
    SharedBaselineCli,
    /// Generated Core client code issues the observable public query.
    PublicGeneratedCore,
    /// The production TypeDef and dispatcher path executes Rust-authored module behaviour.
    ProductionModuleDispatcher,
    /// Exact engine-packaged Rust content exercises runtime or SDK loading behaviour.
    ExactPackagedRuntime,
}

/// One applicable integration fixture bound to its Rust effect and exact claims.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ObservableFixtureProgram {
    /// Stable reviewed fixture selected by the case catalog.
    pub fixture_id: ReviewedFixtureId,
    /// Sole catalog case which executes this fixture context.
    pub case_id: SignoffCaseId,
    /// Production Rust seam used instead of authority-language source code.
    pub boundary: RustObservableBoundary,
    /// Exact semantic predicate observed by the fixture.
    pub predicate: ObservablePredicate,
    /// Complete assertion identities proved by the fixture.
    pub assertion_ids: CanonicalSet<AssertionId>,
    /// Complete capability claims routed through those assertions.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Immutable fixture identity copied from the admitted registry.
    pub fixture_digest: Digest,
}

/// Total validated registry for every applicable integration assertion fixture.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ObservableFixtureProgramRegistry {
    programs: BTreeMap<ReviewedFixtureId, ObservableFixtureProgram>,
    digest: Digest,
}

/// Canonical checked artifact consumed by the engine-free Go sign-off adapter.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ObservableFixtureProgramArtifact {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact target shared by the assertion, fixture, and case inputs.
    pub target_digest: TargetDigest,
    /// Admitted assertion-catalog identity.
    pub assertion_catalog_digest: Digest,
    /// Admitted fixture-registry identity.
    pub fixture_registry_digest: Digest,
    /// Admitted case-catalog identity.
    pub case_catalog_digest: Digest,
    /// Domain-separated identity of the complete program registry.
    pub program_registry_digest: Digest,
    /// Minimal routes in canonical fixture identity order; case claims remain in the bound catalog.
    pub programs: Vec<ObservableFixtureRoute>,
}

/// Minimal portable route consumed alongside the bound case catalog.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ObservableFixtureRoute {
    /// Stable reviewed fixture selected by the case catalog.
    pub fixture_id: ReviewedFixtureId,
    /// Sole catalog case which owns the complete assertion and capability claims.
    pub case_id: SignoffCaseId,
    /// Production Rust seam used instead of authority-language source code.
    pub boundary: RustObservableBoundary,
}

impl ObservableFixtureProgramRegistry {
    /// Borrows programs in stable reviewed-fixture order.
    pub fn programs(&self) -> &BTreeMap<ReviewedFixtureId, ObservableFixtureProgram> {
        &self.programs
    }

    /// Returns the domain-separated identity of the complete executable registry.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }
}

/// Compiles every applicable integration case into one reviewed Rust-owned program.
pub fn compile_observable_fixture_program_registry(
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    catalog: &CaseCatalog,
) -> Result<ObservableFixtureProgramRegistry, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    let mut programs = BTreeMap::new();
    let mut routed_assertions = BTreeSet::new();

    for case in catalog
        .cases()
        .values()
        .filter(|case| case.family == CaseFamily::IntegrationAssertion)
    {
        let CaseProgram::IntegrationAssertion { fixture: selected } = &case.program else {
            diagnostics.push(observable_error(
                Some(case.id.clone()),
                "integration case does not select its reviewed fixture",
            ));
            continue;
        };
        let Some(fixture) = fixtures.fixtures().get(selected) else {
            diagnostics.push(observable_error(
                Some(case.id.clone()),
                "integration case fixture is absent from the reviewed registry",
            ));
            continue;
        };
        let assertion_rows = case
            .assertion_ids
            .iter()
            .filter_map(|id| assertions.assertions().get(id))
            .collect::<Vec<_>>();
        let predicates = assertion_rows
            .iter()
            .map(|assertion| assertion.predicate.clone())
            .collect::<BTreeSet<_>>();
        let contexts = assertion_rows
            .iter()
            .map(|assertion| assertion.fixture_context.clone())
            .collect::<BTreeSet<_>>();
        let claims = CanonicalSet::new(
            assertion_rows
                .iter()
                .flat_map(|assertion| assertion.capability_ids.iter().cloned()),
        );
        let exact_assertions = assertion_rows.len() == case.assertion_ids.len()
            && assertion_rows.iter().all(|assertion| {
                assertion.origin == AssertionOrigin::Applicability
                    && assertion
                        .permitted_families
                        .contains(&AssertionFamily::IntegrationAssertion)
            });
        let exact_fixture = selected == &case.fixture_id
            && fixture.id == case.fixture_id
            && fixture.program == case.program
            && fixture.fixture_digest == case.fixture_digest
            && contexts.len() == 1
            && contexts.first() == Some(&fixture.context_id)
            && predicates.len() == 1
            && claims == case.capability_ids;
        let boundary = predicates
            .first()
            .and_then(|predicate| integration_boundary(&fixture.id, predicate));
        if !exact_assertions || !exact_fixture || boundary.is_none() {
            diagnostics.push(observable_error(
                Some(case.id.clone()),
                "integration fixture lacks one exact reviewed Rust execution route",
            ));
            continue;
        }
        routed_assertions.extend(case.assertion_ids.iter().cloned());
        let program = ObservableFixtureProgram {
            fixture_id: fixture.id.clone(),
            case_id: case.id.clone(),
            boundary: boundary.expect("checked above"),
            predicate: predicates
                .first()
                .cloned()
                .expect("checked exactly one predicate"),
            assertion_ids: case.assertion_ids.clone(),
            capability_ids: case.capability_ids.clone(),
            fixture_digest: fixture.fixture_digest.clone(),
        };
        if programs.insert(fixture.id.clone(), program).is_some() {
            diagnostics.push(observable_error(
                Some(case.id.clone()),
                "reviewed integration fixture is routed by more than one case",
            ));
        }
    }

    let expected_assertions = catalog
        .cases()
        .values()
        .filter(|case| case.family == CaseFamily::IntegrationAssertion)
        .flat_map(|case| case.assertion_ids.iter().cloned())
        .collect::<BTreeSet<_>>();
    if programs.is_empty() || routed_assertions != expected_assertions {
        diagnostics.push(observable_error(
            None,
            "observable fixture registry does not cover the applicable assertion set",
        ));
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }
    let digest = canonical_digest(
        DigestDomain::ConformanceObservableRegistry,
        &programs.values().collect::<Vec<_>>(),
    )
    .map_err(|_| {
        ConformanceDiagnosticSet::new([observable_error(
            None,
            "observable fixture registry cannot be encoded canonically",
        )])
        .expect("one observable diagnostic is non-empty")
    })?;
    Ok(ObservableFixtureProgramRegistry { programs, digest })
}

/// Compiles the portable checked program artifact from the three admitted catalogs.
pub fn build_observable_fixture_program_artifact(
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    catalog: &CaseCatalog,
) -> Result<ObservableFixtureProgramArtifact, ConformanceDiagnosticSet> {
    let registry = compile_observable_fixture_program_registry(assertions, fixtures, catalog)?;
    Ok(ObservableFixtureProgramArtifact {
        format_version: ConformanceFormatVersion::V1,
        target_digest: catalog.target_digest().clone(),
        assertion_catalog_digest: assertions.digest().clone(),
        fixture_registry_digest: fixtures.digest().clone(),
        case_catalog_digest: catalog.digest().clone(),
        program_registry_digest: registry.digest().clone(),
        programs: registry
            .programs()
            .values()
            .map(|program| ObservableFixtureRoute {
                fixture_id: program.fixture_id.clone(),
                case_id: program.case_id.clone(),
                boundary: program.boundary,
            })
            .collect(),
    })
}

/// Authority-language mechanism being translated at the observable boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum AuthorityMechanism {
    /// The authority and Rust use the same public mechanism.
    SharedSdkContract,
    /// The authority mechanism is not idiomatic Rust but its result is observable in Rust.
    NonIdiomaticForRust,
    /// The mechanism belongs solely to another language SDK.
    ForeignSdkOnly,
}

/// Terminal evidence state for one translated authority assertion.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ObservableEvidenceOutcome {
    /// A Rust-owned case passed the reviewed predicate.
    Passed,
    /// A Rust-owned case failed the reviewed predicate.
    Failed,
    /// No passing case was supplied for an applicable assertion.
    Missing,
    /// A foreign-only mechanism was justified without claiming Rust execution.
    JustifiedInapplicable,
}

/// Exact authority-to-Rust translation observation used by applicability admission.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorityTranslationObservation {
    /// Source mechanism class under review.
    pub authority_mechanism: AuthorityMechanism,
    /// Rust production effect, absent only for a foreign-only mechanism.
    pub rust_boundary: Option<RustObservableBoundary>,
    /// Observable contract preserved independently of source-language implementation.
    pub predicate: ObservablePredicate,
    /// Reviewed equivalence decision required only for an unidiomatic source mechanism.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub equivalence_decision: Option<SourceLocator>,
    /// Whether any shared invariant is routed to a Rust-owned assertion.
    pub shared_invariant_routed: bool,
    /// Terminal evidence state.
    pub outcome: ObservableEvidenceOutcome,
    /// Whether capability-local evidence exactly equals the assertion claims.
    pub capability_evidence_complete: bool,
    /// Exact authority drift categories observed before evidence admission.
    pub drift: ObservableAuthorityDrift,
}

/// Bounded authority-scope drift projected into durable conformance evidence.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ObservableAuthorityDrift {
    /// Authority inputs and reviewed assertion scope still agree.
    #[default]
    None,
    /// A new assertion is present in authority inputs.
    Added,
    /// A reviewed assertion disappeared.
    Removed,
    /// Capability membership or mechanism classification changed.
    Reclassified,
}

/// Admits one translated authority observation only when no Rust obligation is lost.
pub fn admit_authority_translation(
    observation: &AuthorityTranslationObservation,
) -> Result<Digest, ConformanceDiagnosticSet> {
    let mechanism_valid = match observation.authority_mechanism {
        AuthorityMechanism::SharedSdkContract => {
            observation.rust_boundary.is_some()
                && observation.equivalence_decision.is_none()
                && observation.outcome == ObservableEvidenceOutcome::Passed
        }
        AuthorityMechanism::NonIdiomaticForRust => {
            observation.rust_boundary.is_some()
                && observation.equivalence_decision.is_some()
                && observation.outcome == ObservableEvidenceOutcome::Passed
        }
        AuthorityMechanism::ForeignSdkOnly => {
            observation.rust_boundary.is_none()
                && observation.equivalence_decision.is_none()
                && observation.outcome == ObservableEvidenceOutcome::JustifiedInapplicable
        }
    };
    if !mechanism_valid
        || !observation.shared_invariant_routed
        || !observation.capability_evidence_complete
        || observation.drift != ObservableAuthorityDrift::None
    {
        return Err(observable_set(
            "authority mechanism loses a Rust effect claim or current assertion route",
        ));
    }
    canonical_digest(DigestDomain::ConformanceObservableRegistry, observation)
        .map_err(|_| observable_set("authority translation cannot be encoded canonically"))
}

/// Fixed common-harness and standalone-client boundary observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HarnessAndClientBoundaryObservation {
    /// Exact common-harness subject checks executed against Rust.
    pub subject_checks: BTreeSet<CommonHarnessCheck>,
    /// Whether the harness's own self-test was misclassified as subject evidence.
    pub harness_self_executed: bool,
    /// Whether every harness claim is within the checked mapping artifact.
    pub harness_claims_are_mapped: bool,
    /// Complete standalone-client case inventory.
    pub standalone_clients: BTreeSet<ClientSignoffCase>,
    /// Whether every standalone project was outside the repository Cargo workspace.
    pub external_workspaces: bool,
    /// Whether every SDK dependency used an immutable packaged identity.
    pub immutable_sdk_dependencies: bool,
    /// Whether any repository path dependency was observed.
    pub repository_path_dependency: bool,
    /// Number of foreign SDK suites executed by the observation.
    pub foreign_suite_runs: u32,
}

/// Validates the fixed harness and standalone-client boundaries.
pub fn admit_harness_and_client_boundary(
    observation: &HarnessAndClientBoundaryObservation,
) -> Result<Digest, ConformanceDiagnosticSet> {
    if observation.subject_checks != super::required_common_harness_checks()
        || observation.harness_self_executed
        || !observation.harness_claims_are_mapped
        || observation.standalone_clients != super::required_standalone_client_cases()
        || !observation.external_workspaces
        || !observation.immutable_sdk_dependencies
        || observation.repository_path_dependency
        || observation.foreign_suite_runs != 0
    {
        return Err(observable_set(
            "harness or standalone-client observation crosses its reviewed boundary",
        ));
    }
    canonical_digest(DigestDomain::ConformanceObservableRegistry, observation)
        .map_err(|_| observable_set("harness and client boundary cannot be encoded canonically"))
}

/// Production module semantic required by exact-target module authoring.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleSemantic {
    /// Module initialization.
    Initialization,
    /// Development lifecycle.
    Development,
    /// Code generation.
    Generation,
    /// Module loading.
    Loading,
    /// Module execution.
    Execution,
    /// Dependency use.
    Dependency,
    /// Constructor dispatch.
    Constructor,
    /// Synchronous function dispatch.
    Synchronous,
    /// Asynchronous function dispatch.
    Asynchronous,
    /// Stateful object dispatch.
    Stateful,
    /// Generated Core handle use.
    Core,
    /// Self-call routing.
    SelfCall,
    /// Dependency-object routing.
    DependencyObject,
    /// Interface argument or return dispatch.
    Interface,
    /// Enum input or output dispatch.
    Enum,
    /// Default and omission semantics.
    Default,
    /// Typed error propagation.
    Error,
    /// Panic containment.
    Panic,
    /// Cancellation propagation.
    Cancellation,
    /// Concurrent independent calls.
    Concurrent,
}

/// Complete production module-authoring observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSemanticMatrixObservation {
    /// Exact required semantic set observed through production dispatch.
    pub semantics: BTreeSet<ModuleSemantic>,
    /// Fixed grouped module case inventory which supplied those observations.
    pub grouped_cases: BTreeSet<ModuleSignoffCase>,
    /// Whether TypeDef registration and the production dispatcher were used.
    pub production_dispatcher: bool,
    /// Whether the packaged self-consumer resolved only artifact SDK content.
    pub artifact_sdk_content_only: bool,
    /// Whether a fixture-only dispatcher substituted for production code.
    pub fixture_dispatcher_used: bool,
}

/// Returns every module semantic required by the production authoring matrix.
pub fn required_module_semantics() -> BTreeSet<ModuleSemantic> {
    use ModuleSemantic as Semantic;
    BTreeSet::from([
        Semantic::Initialization,
        Semantic::Development,
        Semantic::Generation,
        Semantic::Loading,
        Semantic::Execution,
        Semantic::Dependency,
        Semantic::Constructor,
        Semantic::Synchronous,
        Semantic::Asynchronous,
        Semantic::Stateful,
        Semantic::Core,
        Semantic::SelfCall,
        Semantic::DependencyObject,
        Semantic::Interface,
        Semantic::Enum,
        Semantic::Default,
        Semantic::Error,
        Semantic::Panic,
        Semantic::Cancellation,
        Semantic::Concurrent,
    ])
}

/// Validates complete module semantics and artifact-only packaged consumption.
pub fn admit_module_semantic_matrix(
    observation: &ModuleSemanticMatrixObservation,
) -> Result<Digest, ConformanceDiagnosticSet> {
    if observation.semantics != required_module_semantics()
        || observation.grouped_cases != super::required_module_authoring_cases()
        || !observation.production_dispatcher
        || !observation.artifact_sdk_content_only
        || observation.fixture_dispatcher_used
    {
        return Err(observable_set(
            "module authoring omits a production semantic or substitutes fixture content",
        ));
    }
    canonical_digest(DigestDomain::ConformanceObservableRegistry, observation)
        .map_err(|_| observable_set("module semantic matrix cannot be encoded canonically"))
}

/// Public generated-API observation for Core and standalone clients.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratedApiBoundaryObservation {
    /// Exact representative generated Core shape inventory.
    pub core_shapes: BTreeSet<CoreCaseShape>,
    /// Exact standalone generated-client case inventory.
    pub client_cases: BTreeSet<ClientSignoffCase>,
    /// Whether the remote client dependency was selected by a full immutable revision.
    pub immutable_remote_revision: bool,
    /// Whether schema drift changed SDK-owned generated content.
    pub owned_generated_content_changed: bool,
    /// Whether schema regeneration preserved all caller-authored content.
    pub authored_content_preserved: bool,
    /// Whether the Core observation used only the public generated API.
    pub public_core_query: bool,
    /// Whether the bound module observation used the generated namespaced API.
    pub generated_namespaced_module_query: bool,
    /// Whether an ambient repository path dependency was observed.
    pub ambient_path_dependency: bool,
}

/// Validates public generated Core and standalone-client boundaries.
pub fn admit_generated_api_boundary(
    observation: &GeneratedApiBoundaryObservation,
) -> Result<Digest, ConformanceDiagnosticSet> {
    if observation.core_shapes != super::required_core_shapes()
        || observation.client_cases != super::required_standalone_client_cases()
        || !observation.immutable_remote_revision
        || !observation.owned_generated_content_changed
        || !observation.authored_content_preserved
        || !observation.public_core_query
        || !observation.generated_namespaced_module_query
        || observation.ambient_path_dependency
    {
        return Err(observable_set(
            "generated API observation omits a shape query or immutable ownership boundary",
        ));
    }
    canonical_digest(DigestDomain::ConformanceObservableRegistry, observation)
        .map_err(|_| observable_set("generated API boundary cannot be encoded canonically"))
}

fn integration_boundary(
    fixture_id: &ReviewedFixtureId,
    predicate: &ObservablePredicate,
) -> Option<RustObservableBoundary> {
    if predicate == &ObservablePredicate::Query(QueryObservation::Core) {
        return Some(RustObservableBoundary::PublicGeneratedCore);
    }
    let group = fixture_id.as_str().split('/').nth(2)?;
    if CLI_GROUPS.contains(&group) {
        Some(RustObservableBoundary::SharedBaselineCli)
    } else if PACKAGED_RUNTIME_GROUPS.contains(&group) {
        Some(RustObservableBoundary::ExactPackagedRuntime)
    } else if MODULE_DISPATCH_GROUPS.contains(&group) {
        Some(RustObservableBoundary::ProductionModuleDispatcher)
    } else {
        None
    }
}

const CLI_GROUPS: &[&str] = &[
    "cli-module",
    "cli-module-init",
    "cli-module-sdk",
    "cli-sdk-init-dynamic",
    "module-config",
    "module-config-compat",
    "module-dependency-cli",
    "module-introspection-cli",
    "module-loading",
    "module-terminal",
    "module-up",
    "workspace-modules",
];

const PACKAGED_RUNTIME_GROUPS: &[&str] = &[
    "module-custom-sdk",
    "module-engine-version",
    "module-runtime-behavior",
    "module-runtime-codegen",
];

const MODULE_DISPATCH_GROUPS: &[&str] = &[
    "module-benchmark",
    "module-call",
    "module-constructor",
    "module-current-module",
    "module-definition",
    "module-dependency-runtime",
    "module-deprecation",
    "module-error",
    "module-iface",
    "module-path-inputs",
    "module-private-deps",
    "module-self-calls",
    "module-type",
    "module-validation",
];

fn observable_error(case_id: Option<SignoffCaseId>, detail: &'static str) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::ConformanceCaseForbidden,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Case),
            case_id,
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn observable_set(detail: &'static str) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([observable_error(None, detail)])
        .expect("one observable diagnostic is non-empty")
}
