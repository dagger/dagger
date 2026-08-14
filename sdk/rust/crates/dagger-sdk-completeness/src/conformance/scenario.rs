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

/// One Rust-runner registration compiled into the checked implementation.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RustScenarioRegistration {
    /// Stable selector accepted by the Rust runner.
    pub realization_id: ScenarioRealizationId,
    /// Sole production boundary implemented by the registered function.
    pub boundary: RustObservableBoundary,
    /// Reviewed fixture identity for a fixture realization; absent for generated Core.
    pub fixture_id: Option<ReviewedFixtureId>,
}

/// Validated Rust-runner registrations keyed by stable realization identity.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct RustScenarioRegistry {
    registrations: BTreeMap<ScenarioRealizationId, RustScenarioRegistration>,
}

impl RustScenarioRegistry {
    /// Constructs a registry while rejecting duplicate identities.
    pub fn new(
        registrations: impl IntoIterator<Item = RustScenarioRegistration>,
    ) -> Result<Self, ConformanceDiagnosticSet> {
        let mut values = BTreeMap::new();
        for registration in registrations {
            if values
                .insert(registration.realization_id.clone(), registration)
                .is_some()
            {
                return Err(scenario_set(
                    None,
                    "Rust scenario realization identity is duplicated",
                ));
            }
        }
        Ok(Self {
            registrations: values,
        })
    }

    /// Borrows checked registrations in canonical identity order.
    pub fn registrations(&self) -> &BTreeMap<ScenarioRealizationId, RustScenarioRegistration> {
        &self.registrations
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

/// Compiles only a total, claim-exact manifest backed by checked Rust registrations.
pub fn compile_rust_first_conformance_manifest(
    input: RustFirstConformanceManifestInput,
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    catalog: &CaseCatalog,
    registry: &RustScenarioRegistry,
) -> Result<RustFirstConformanceManifest, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    if input.target_digest != *catalog.target_digest()
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
    let mut used_registrations = BTreeSet::new();
    for scenario in input.scenarios {
        let case_id = scenario.spine.id.clone();
        if !scenario_inputs_are_valid(&scenario.spine.inputs)
            || !scenario_matches_catalog(&scenario, assertions, fixtures, catalog, &expected_routes)
            || !realization_is_registered(&scenario, registry, &mut used_registrations)
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
    if used_registrations.len() != scenarios.len() {
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
    let actual_fixture_inputs = CanonicalSet::new(
        scenario
            .spine
            .inputs
            .iter()
            .filter(|input| input.semantics == ScenarioInputSemantics::FixtureMaterial)
            .cloned(),
    );
    scenario.spine.authority_anchors == anchors
        && scenario.spine.source_fingerprints == fingerprints
        && scenario.spine.subject
            == (ScenarioSubject {
                boundary: program.boundary,
                context: fixture.context_id.clone(),
            })
        && actual_fixture_inputs == expected_fixture_inputs
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

fn realization_is_registered(
    scenario: &RustFirstScenario,
    registry: &RustScenarioRegistry,
    used: &mut BTreeSet<ScenarioRealizationId>,
) -> bool {
    let (realization_id, expected_fixture, generated_core) = match &scenario.realization {
        RustScenarioRealization::RealizationRequired => return false,
        RustScenarioRealization::GeneratedCore {
            realization_id,
            schema_coordinate,
        } => (realization_id, None, !schema_coordinate.as_str().is_empty()),
        RustScenarioRealization::ReviewedRustFixture {
            realization_id,
            fixture_id,
        } => (realization_id, Some(fixture_id), false),
    };
    let Some(registration) = registry.registrations().get(realization_id) else {
        return false;
    };
    let fixture_matches = match expected_fixture {
        Some(fixture_id) => registration.fixture_id.as_ref() == Some(fixture_id),
        None => registration.fixture_id.is_none(),
    };
    fixture_matches
        && registration.boundary == scenario.spine.subject.boundary
        && (generated_core
            == (registration.boundary == RustObservableBoundary::PublicGeneratedCore))
        && used.insert(realization_id.clone())
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
