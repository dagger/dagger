//! Minimal Rust-first scenario manifest for authority-selected integration behaviour.
//!
//! The manifest keeps only the semantic spine which must survive the authority language:
//! provenance, subject, immutable inputs, and expected observation. Execution remains explicitly
//! Rust-owned. A generated candidate is therefore not admissible until it names one realization
//! registered by the Rust runner; a boundary label or Go selector can never satisfy that check.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{CanonicalSet, CapabilityId, Digest, SourceLocator, TargetDigest};

use super::{
    AssertionCatalog, AssertionFamily, AssertionId, AssertionOrigin, AuthorityAnchor, CaseCatalog,
    CaseFamily, CaseProgram, ConformanceDiagnostic, ConformanceDiagnosticCode,
    ConformanceDiagnosticSet, ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase,
    FixtureContextId, FixtureRegistry, ObservablePredicate, ReviewedFixtureId,
    RustObservableBoundary, ScenarioRealizationId, SignoffCaseId,
    compile_observable_fixture_program_registry,
};

/// Portability classification which never changes Rust execution or evidence.
#[derive(
    Clone, Copy, Debug, Default, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize,
)]
#[serde(rename_all = "kebab-case")]
pub enum ScenarioPortability {
    /// The scenario exists solely for the Rust conformance manifest.
    #[default]
    RustOnly,
    /// The small semantic spine may later inform a reviewed `sdk-sdk` client profile.
    SdkSdkCandidate,
}

/// Presence semantics retained for one immutable scenario input.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ScenarioInputSemantics {
    /// The public input is deliberately absent.
    Omitted,
    /// The public input is deliberately supplied, including false, zero, empty, or null.
    Explicit,
    /// Complex reviewed setup is supplied by immutable fixture material.
    FixtureMaterial,
}

/// One language-neutral scenario input and its content identity when present.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ScenarioInput {
    /// Stable semantic input name rather than a source-language expression.
    pub name: SourceLocator,
    /// Whether the input is omitted, explicit, or delegated to reviewed fixture material.
    pub semantics: ScenarioInputSemantics,
    /// Immutable value or fixture-material identity; absent only for an omitted input.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub value_digest: Option<Digest>,
}

/// Public or production subject exercised by one scenario.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ScenarioSubject {
    /// Rust production boundary which owns the observable effect.
    pub boundary: RustObservableBoundary,
    /// Semantic setup context shared by the assertion and reviewed fixture.
    pub context: FixtureContextId,
}

/// Deliberately small source-language-neutral portion of one conformance scenario.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ScenarioSpine {
    /// Stable identity shared with the closed case catalog.
    pub id: SignoffCaseId,
    /// Exact pinned authority coordinates retained for review and drift detection.
    pub authority_anchors: CanonicalSet<AuthorityAnchor>,
    /// Exact authority content identities retained for drift detection.
    pub source_fingerprints: CanonicalSet<Digest>,
    /// Rust subject and setup context, independent of source-language syntax.
    pub subject: ScenarioSubject,
    /// Immutable input presence and content semantics.
    pub inputs: CanonicalSet<ScenarioInput>,
    /// Exact semantic observation which the Rust realization must produce.
    pub expected: ObservablePredicate,
}

/// Exactly one Rust-owned way to execute a scenario spine.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum RustScenarioRealization {
    /// Generated semantic candidate with no registered executable Rust implementation.
    RealizationRequired,
    /// Straightforward checked schema coordinate realized through public generated Core.
    GeneratedCore {
        /// Registered Rust runner selector.
        realization_id: ScenarioRealizationId,
        /// Public schema coordinate implemented by generated Rust code.
        schema_coordinate: SourceLocator,
    },
    /// Reviewed idiomatic Rust fixture for lifecycle, module, CLI, concurrency, or complex setup.
    ReviewedRustFixture {
        /// Registered Rust runner selector.
        realization_id: ScenarioRealizationId,
        /// Exact reviewed fixture whose immutable setup is used.
        fixture_id: ReviewedFixtureId,
    },
}

/// One manifest row joining a semantic spine to claims and one Rust realization.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RustFirstScenario {
    /// Minimal language-neutral contract.
    pub spine: ScenarioSpine,
    /// Complete assertion set proved by the realization.
    pub assertion_ids: CanonicalSet<AssertionId>,
    /// Complete capability set reached through those assertions.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Advisory portability classification with no execution semantics.
    pub portability: ScenarioPortability,
    /// Sole Rust-owned executable realization.
    pub realization: RustScenarioRealization,
}

/// Authored manifest input; duplicate scenario IDs remain observable until compilation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RustFirstConformanceManifestInput {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact target shared by the admitted catalogs.
    pub target_digest: TargetDigest,
    /// Exact assertion catalog identity.
    pub assertion_catalog_digest: Digest,
    /// Exact fixture registry identity.
    pub fixture_registry_digest: Digest,
    /// Exact case catalog identity.
    pub case_catalog_digest: Digest,
    /// Authored scenario rows.
    pub scenarios: Vec<RustFirstScenario>,
}

/// One reviewed binding from an authority scenario to checked executable Rust code.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RustScenarioRegistration {
    /// Exact scenario case realized by this registration.
    pub scenario_id: SignoffCaseId,
    /// Sole checked Rust realization selected for the scenario.
    pub realization: RustScenarioRealization,
}

/// Authored, source-bound realization registry which may remain partial during review.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RustScenarioRegistryInput {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact target shared by the scenario candidates.
    pub target_digest: TargetDigest,
    /// Domain-separated identity of the complete generated candidate queue.
    pub scenario_candidate_digest: Digest,
    /// Exact bytes of the checked Rust runner containing the registered entrypoints.
    pub runner_source_digest: Digest,
    /// Reviewed registrations in canonical scenario order.
    pub registrations: Vec<RustScenarioRegistration>,
}

/// Validated Rust-runner registrations keyed by scenario and realization identity.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RustScenarioRegistry {
    registrations: BTreeMap<SignoffCaseId, RustScenarioRegistration>,
    realization_ids: BTreeMap<ScenarioRealizationId, SignoffCaseId>,
    runner_source_digest: Digest,
    digest: Digest,
}

impl RustScenarioRegistry {
    /// Borrows checked registrations in canonical scenario order.
    pub fn registrations(&self) -> &BTreeMap<SignoffCaseId, RustScenarioRegistration> {
        &self.registrations
    }

    /// Borrows the exact checked runner source identity.
    pub fn runner_source_digest(&self) -> &Digest {
        &self.runner_source_digest
    }

    /// Returns the domain-separated identity of the reviewed registry.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }
}

/// Validated complete manifest in canonical scenario order.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RustFirstConformanceManifest {
    scenarios: BTreeMap<SignoffCaseId, RustFirstScenario>,
    digest: Digest,
}

impl RustFirstConformanceManifest {
    /// Borrows all scenarios in canonical case identity order.
    pub fn scenarios(&self) -> &BTreeMap<SignoffCaseId, RustFirstScenario> {
        &self.scenarios
    }

    /// Returns the domain-separated identity of the complete manifest.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }
}

/// Creates realization-required candidates without granting executable status.
pub fn scaffold_rust_first_conformance_manifest(
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    catalog: &CaseCatalog,
) -> Result<RustFirstConformanceManifestInput, ConformanceDiagnosticSet> {
    let routes = compile_observable_fixture_program_registry(assertions, fixtures, catalog)?;
    let mut scenarios = Vec::with_capacity(routes.programs().len());
    for program in routes.programs().values() {
        let case = catalog
            .cases()
            .get(&program.case_id)
            .expect("observable registry was compiled from this catalog");
        let fixture = fixtures
            .fixtures()
            .get(&program.fixture_id)
            .expect("observable registry was compiled from this fixture registry");
        let assertion_rows = case
            .assertion_ids
            .iter()
            .map(|id| {
                assertions
                    .assertions()
                    .get(id)
                    .expect("observable registry admitted every assertion")
            })
            .collect::<Vec<_>>();
        let authority_anchors = CanonicalSet::new(
            assertion_rows
                .iter()
                .flat_map(|assertion| assertion.authority_anchors.iter().cloned()),
        );
        let source_fingerprints = CanonicalSet::new(
            assertion_rows
                .iter()
                .flat_map(|assertion| assertion.source_fingerprints.iter().cloned()),
        );
        let inputs = CanonicalSet::new(fixture.immutable_inputs.iter().enumerate().map(
            |(index, digest)| {
                ScenarioInput {
                    name: SourceLocator::new(format!("fixture-input/{index}"))
                        .expect("canonical fixture input locator"),
                    semantics: ScenarioInputSemantics::FixtureMaterial,
                    value_digest: Some(digest.clone()),
                }
            },
        ));
        scenarios.push(RustFirstScenario {
            spine: ScenarioSpine {
                id: case.id.clone(),
                authority_anchors,
                source_fingerprints,
                subject: ScenarioSubject {
                    boundary: program.boundary,
                    context: fixture.context_id.clone(),
                },
                inputs,
                expected: program.predicate.clone(),
            },
            assertion_ids: case.assertion_ids.clone(),
            capability_ids: case.capability_ids.clone(),
            portability: ScenarioPortability::RustOnly,
            realization: RustScenarioRealization::RealizationRequired,
        });
    }
    Ok(RustFirstConformanceManifestInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest: catalog.target_digest().clone(),
        assertion_catalog_digest: assertions.digest().clone(),
        fixture_registry_digest: fixtures.digest().clone(),
        case_catalog_digest: catalog.digest().clone(),
        scenarios,
    })
}

/// Returns the domain-separated identity of the complete generated candidate queue.
pub fn rust_scenario_candidate_digest(
    candidates: &RustFirstConformanceManifestInput,
) -> Result<Digest, ConformanceDiagnosticSet> {
    canonical_digest(DigestDomain::ConformanceScenarioCandidates, candidates)
        .map_err(|_| scenario_set(None, "scenario candidates cannot be encoded canonically"))
}

/// Creates an empty reviewed registry bound to the current queue and runner source.
///
/// An empty registry is a valid review work surface, but it cannot admit a conformance manifest.
pub fn scaffold_rust_scenario_registry(
    candidates: &RustFirstConformanceManifestInput,
    runner_source_digest: Digest,
) -> Result<RustScenarioRegistryInput, ConformanceDiagnosticSet> {
    Ok(RustScenarioRegistryInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest: candidates.target_digest.clone(),
        scenario_candidate_digest: rust_scenario_candidate_digest(candidates)?,
        runner_source_digest,
        registrations: Vec::new(),
    })
}

/// Validates reviewed registrations against the current candidates, case catalog, and source.
///
/// Partial registries are admitted so review can advance in bounded slices. Only
/// [`compile_rust_first_conformance_manifest`] requires total coverage.
pub fn compile_rust_scenario_registry(
    input: RustScenarioRegistryInput,
    candidates: &RustFirstConformanceManifestInput,
    catalog: &CaseCatalog,
    observed_runner_source_digest: &Digest,
) -> Result<RustScenarioRegistry, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    if input.format_version != ConformanceFormatVersion::V1
        || candidates.format_version != ConformanceFormatVersion::V1
        || candidates.target_digest != *catalog.target_digest()
        || candidates.case_catalog_digest != *catalog.digest()
        || input.target_digest != candidates.target_digest
        || input.scenario_candidate_digest != rust_scenario_candidate_digest(candidates)?
        || input.runner_source_digest != *observed_runner_source_digest
    {
        diagnostics.push(scenario_error(
            None,
            "Rust scenario registry identity or runner source is stale",
        ));
    }

    let candidate_by_id = candidates
        .scenarios
        .iter()
        .map(|scenario| (scenario.spine.id.clone(), scenario))
        .collect::<BTreeMap<_, _>>();
    if candidate_by_id.len() != candidates.scenarios.len()
        || candidates
            .scenarios
            .windows(2)
            .any(|pair| pair[0].spine.id >= pair[1].spine.id)
        || candidates.scenarios.iter().any(|scenario| {
            !matches!(
                scenario.realization,
                RustScenarioRealization::RealizationRequired
            )
        })
    {
        diagnostics.push(scenario_error(
            None,
            "Rust scenario candidates are ambiguous non-canonical or already realized",
        ));
    }
    let mut registrations = BTreeMap::new();
    let mut realization_ids = BTreeMap::new();
    let mut previous_scenario_id = None;
    for registration in input.registrations {
        let scenario_id = registration.scenario_id.clone();
        if previous_scenario_id
            .as_ref()
            .is_some_and(|previous| previous >= &scenario_id)
        {
            diagnostics.push(scenario_error(
                Some(scenario_id.clone()),
                "Rust scenario registrations are not in canonical unique order",
            ));
        }
        previous_scenario_id = Some(scenario_id.clone());

        let Some(candidate) = candidate_by_id.get(&scenario_id) else {
            diagnostics.push(scenario_error(
                Some(scenario_id.clone()),
                "Rust scenario registration does not name a selected candidate",
            ));
            continue;
        };
        if !registration_matches_candidate(&registration, candidate, catalog) {
            diagnostics.push(scenario_error(
                Some(scenario_id.clone()),
                "Rust scenario registration is stale ambiguous or not executable",
            ));
        }
        let Some(realization_id) = realization_id(&registration.realization) else {
            diagnostics.push(scenario_error(
                Some(scenario_id.clone()),
                "realization-required cannot enter the reviewed Rust registry",
            ));
            continue;
        };
        if realization_ids
            .insert(realization_id.clone(), scenario_id.clone())
            .is_some()
        {
            diagnostics.push(scenario_error(
                Some(scenario_id.clone()),
                "Rust scenario realization identity is duplicated",
            ));
        }
        if registrations
            .insert(scenario_id.clone(), registration)
            .is_some()
        {
            diagnostics.push(scenario_error(
                Some(scenario_id),
                "Rust scenario identity is registered more than once",
            ));
        }
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }
    let digest = canonical_digest(
        DigestDomain::ConformanceScenarioRegistry,
        &(
            &input.target_digest,
            &input.scenario_candidate_digest,
            &input.runner_source_digest,
            registrations.values().collect::<Vec<_>>(),
        ),
    )
    .map_err(|_| scenario_set(None, "Rust scenario registry cannot be encoded canonically"))?;
    Ok(RustScenarioRegistry {
        registrations,
        realization_ids,
        runner_source_digest: input.runner_source_digest,
        digest,
    })
}

/// Applies reviewed registrations without changing any unreviewed candidate.
pub fn apply_rust_scenario_registry(
    mut candidates: RustFirstConformanceManifestInput,
    registry: &RustScenarioRegistry,
) -> RustFirstConformanceManifestInput {
    for scenario in &mut candidates.scenarios {
        if let Some(registration) = registry.registrations.get(&scenario.spine.id) {
            scenario.realization = registration.realization.clone();
        }
    }
    candidates
}

/// Compiles only a total, claim-exact manifest backed by checked Rust registrations.
pub fn compile_rust_first_conformance_manifest(
    input: RustFirstConformanceManifestInput,
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    catalog: &CaseCatalog,
    registry: &RustScenarioRegistry,
) -> Result<RustFirstConformanceManifest, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    if input.format_version != ConformanceFormatVersion::V1
        || input.target_digest != *catalog.target_digest()
        || input.target_digest != *assertions.target_digest()
        || input.assertion_catalog_digest != *assertions.digest()
        || input.fixture_registry_digest != *fixtures.digest()
        || input.case_catalog_digest != *catalog.digest()
    {
        diagnostics.push(scenario_error(
            None,
            "scenario manifest catalog identity drifted",
        ));
    }

    let expected_routes =
        compile_observable_fixture_program_registry(assertions, fixtures, catalog)?;
    let expected_case_ids = expected_routes
        .programs()
        .values()
        .map(|program| program.case_id.clone())
        .collect::<BTreeSet<_>>();
    let mut scenarios = BTreeMap::new();
    for scenario in input.scenarios {
        let case_id = scenario.spine.id.clone();
        if !scenario_inputs_are_valid(&scenario.spine.inputs)
            || !scenario_matches_catalog(&scenario, assertions, fixtures, catalog, &expected_routes)
            || !realization_is_registered(&scenario, registry)
        {
            diagnostics.push(scenario_error(
                Some(case_id.clone()),
                "scenario spine or Rust realization is missing stale ambiguous or overbroad",
            ));
        }
        if scenarios.insert(case_id.clone(), scenario).is_some() {
            diagnostics.push(scenario_error(
                Some(case_id),
                "scenario identity is duplicated",
            ));
        }
    }
    if scenarios.keys().cloned().collect::<BTreeSet<_>>() != expected_case_ids {
        diagnostics.push(scenario_error(
            None,
            "scenario manifest does not equal the applicable integration case inventory",
        ));
    }
    if registry
        .registrations
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>()
        != expected_case_ids
        || registry.realization_ids.len() != scenarios.len()
    {
        diagnostics.push(scenario_error(
            None,
            "scenario realizations are not one-to-one with manifest rows",
        ));
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }
    let digest = canonical_digest(
        DigestDomain::ConformanceScenarioManifest,
        &scenarios.values().collect::<Vec<_>>(),
    )
    .map_err(|_| scenario_set(None, "scenario manifest cannot be encoded canonically"))?;
    Ok(RustFirstConformanceManifest { scenarios, digest })
}

fn scenario_matches_catalog(
    scenario: &RustFirstScenario,
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    catalog: &CaseCatalog,
    routes: &super::ObservableFixtureProgramRegistry,
) -> bool {
    let Some(case) = catalog.cases().get(&scenario.spine.id) else {
        return false;
    };
    if case.family != CaseFamily::IntegrationAssertion
        || scenario.assertion_ids != case.assertion_ids
        || scenario.capability_ids != case.capability_ids
    {
        return false;
    }
    let CaseProgram::IntegrationAssertion {
        fixture: fixture_id,
    } = &case.program
    else {
        return false;
    };
    let Some(fixture) = fixtures.fixtures().get(fixture_id) else {
        return false;
    };
    let Some(program) = routes.programs().get(fixture_id) else {
        return false;
    };
    let rows = case
        .assertion_ids
        .iter()
        .filter_map(|id| assertions.assertions().get(id))
        .collect::<Vec<_>>();
    if rows.len() != case.assertion_ids.len()
        || rows.iter().any(|row| {
            row.origin != AssertionOrigin::Applicability
                || !row
                    .permitted_families
                    .contains(&AssertionFamily::IntegrationAssertion)
        })
    {
        return false;
    }
    let anchors = CanonicalSet::new(
        rows.iter()
            .flat_map(|row| row.authority_anchors.iter().cloned()),
    );
    let fingerprints = CanonicalSet::new(
        rows.iter()
            .flat_map(|row| row.source_fingerprints.iter().cloned()),
    );
    let expected_fixture_inputs = CanonicalSet::new(
        fixture
            .immutable_inputs
            .iter()
            .enumerate()
            .map(|(index, digest)| ScenarioInput {
                name: SourceLocator::new(format!("fixture-input/{index}"))
                    .expect("canonical fixture input locator"),
                semantics: ScenarioInputSemantics::FixtureMaterial,
                value_digest: Some(digest.clone()),
            }),
    );
    scenario.spine.authority_anchors == anchors
        && scenario.spine.source_fingerprints == fingerprints
        && scenario.spine.subject
            == (ScenarioSubject {
                boundary: program.boundary,
                context: fixture.context_id.clone(),
            })
        && scenario.spine.inputs == expected_fixture_inputs
        && scenario.spine.expected == program.predicate
}

fn scenario_inputs_are_valid(inputs: &CanonicalSet<ScenarioInput>) -> bool {
    !inputs.is_empty()
        && inputs.iter().all(|input| match input.semantics {
            ScenarioInputSemantics::Omitted => input.value_digest.is_none(),
            ScenarioInputSemantics::Explicit | ScenarioInputSemantics::FixtureMaterial => {
                input.value_digest.is_some()
            }
        })
}

fn registration_matches_candidate(
    registration: &RustScenarioRegistration,
    candidate: &RustFirstScenario,
    catalog: &CaseCatalog,
) -> bool {
    if registration.scenario_id != candidate.spine.id
        || !matches!(
            candidate.realization,
            RustScenarioRealization::RealizationRequired
        )
    {
        return false;
    }
    let Some(case) = catalog.cases().get(&candidate.spine.id) else {
        return false;
    };
    let CaseProgram::IntegrationAssertion { fixture } = &case.program else {
        return false;
    };
    match &registration.realization {
        RustScenarioRealization::RealizationRequired => false,
        RustScenarioRealization::GeneratedCore {
            schema_coordinate, ..
        } => {
            candidate.spine.subject.boundary == RustObservableBoundary::PublicGeneratedCore
                && !schema_coordinate.as_str().is_empty()
        }
        RustScenarioRealization::ReviewedRustFixture { fixture_id, .. } => {
            candidate.spine.subject.boundary != RustObservableBoundary::PublicGeneratedCore
                && fixture_id == fixture
        }
    }
}

fn realization_id(realization: &RustScenarioRealization) -> Option<&ScenarioRealizationId> {
    match realization {
        RustScenarioRealization::RealizationRequired => None,
        RustScenarioRealization::GeneratedCore { realization_id, .. }
        | RustScenarioRealization::ReviewedRustFixture { realization_id, .. } => {
            Some(realization_id)
        }
    }
}

fn realization_is_registered(
    scenario: &RustFirstScenario,
    registry: &RustScenarioRegistry,
) -> bool {
    registry
        .registrations()
        .get(&scenario.spine.id)
        .is_some_and(|registration| registration.realization == scenario.realization)
}

fn scenario_error(case_id: Option<SignoffCaseId>, detail: &'static str) -> ConformanceDiagnostic {
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

fn scenario_set(case_id: Option<SignoffCaseId>, detail: &'static str) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([scenario_error(case_id, detail)])
        .expect("one scenario diagnostic is non-empty")
}
