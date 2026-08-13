//! Rust-observable assertion compilation and authority drift accounting.
//!
//! Assertions describe observable contracts without embedding source-language code. The compiler
//! binds each scope assertion to its exact authority coordinates and keeps semantically equal
//! assertions grouped only when both their normalized predicate and fixture context agree.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{CanonicalSet, CapabilityId, Digest, SourceLocator, TargetDigest};

use super::{
    AssertionId, AuthorityAnchor, ConformanceDiagnostic, ConformanceDiagnosticCode,
    ConformanceDiagnosticSet, ConformanceFormatVersion, ConformanceScope, DiagnosticCoordinate,
    DiagnosticPhase, FixtureContextId,
};

/// Closed sign-off family which may exercise one assertion.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum AssertionFamily {
    /// Portable subject checks imported from the shared SDK harness.
    CommonHarness,
    /// Stable default connector lifecycle and transport behaviour.
    StableConnector,
    /// Representative generated Core API shapes.
    CoreGeneratedApi,
    /// Rust integration with engine-owned SDK lifecycle hooks.
    EngineIntegration,
    /// Rust-authored module execution and packaging.
    ModuleAuthoring,
    /// Standalone generated-client lifecycle and queries.
    StandaloneClient,
    /// Selected Go-client observations expressed through public Rust APIs.
    DefinitiveGoClient,
    /// Remaining authority-selected integration assertions.
    IntegrationAssertion,
}

/// Observable successful-result contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ResultObservation {
    /// The returned scalar or structured value must match exactly.
    ExactValue,
    /// Collection order, cardinality, and element shape are observable.
    CollectionShape,
    /// A mutation must be visible to the subsequent public operation.
    MutationVisible,
    /// Generated public types and methods must be present and usable.
    GeneratedSurface,
}

/// Observable typed-error contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum TypedErrorObservation {
    /// The stable error category must be preserved.
    Category,
    /// The complete public typed-error field set must be preserved.
    Fields,
    /// Empty process output remains distinguishable from absent error data.
    EmptyOutput,
    /// Non-execution failures must not be misclassified as execution errors.
    NonExecutionSeparation,
}

/// Observable lifecycle contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum LifecycleObservation {
    /// Initialization must produce the reviewed persistent state.
    Initialize,
    /// A generated or installed subject must load successfully.
    Load,
    /// Invocation must cross the reviewed runtime boundary and return.
    Invoke,
    /// Shutdown must close resources and reap owned processes.
    CloseAndReap,
}

/// Observable filesystem contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FilesystemObservation {
    /// Exact generated or preserved file content is observable.
    Content,
    /// The SDK may modify only files within its declared ownership boundary.
    Ownership,
    /// Path resolution must remain within the reviewed root.
    PathBoundary,
    /// Pre-existing user content must remain unchanged.
    Preservation,
}

/// Observable public-query contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum QueryObservation {
    /// A query uses the generated Core surface.
    Core,
    /// A query uses a namespaced module surface.
    Module,
    /// A query resolves a declared module dependency.
    Dependency,
    /// A query observes schema or module introspection metadata.
    Introspection,
}

/// Observable metadata contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum MetadataObservation {
    /// Type, function, or module definitions must match the authority.
    Definition,
    /// Generated source coordinates must remain attributable.
    SourceMap,
    /// Deprecation state and messages must remain observable.
    Deprecation,
    /// Version metadata must bind the exact target.
    Version,
}

/// Observable omission/default contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum OmissionObservation {
    /// An omitted input must remain distinguishable from an explicit value.
    Omitted,
    /// An explicit value, including `false` or zero, must be transmitted.
    ExplicitValue,
    /// Explicit nullability must survive query construction and decoding.
    Null,
    /// Default application must match the schema rather than host-language zero values.
    Default,
}

/// Observable isolation contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum IsolationObservation {
    /// Concurrent calls must not share call-local state.
    Call,
    /// Fixture mutations must remain within one case workspace.
    Workspace,
    /// Dependency state must not leak between module identities.
    Dependency,
    /// Cancellation must affect only its owning operation and elect one result.
    Cancellation,
}

/// Observable compatibility contract.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CompatibilityObservation {
    /// The observed behaviour must bind the exact Dagger target version.
    TargetVersion,
    /// Configuration parsing and selection must remain backward compatible.
    Configuration,
    /// A deliberately retained legacy runtime path must remain isolated.
    LegacyRuntime,
    /// Remote or packaged input must be selected by immutable identity.
    ImmutableReference,
}

/// Closed semantic predicate vocabulary retained in the reviewed assertion artifact.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind", content = "observation")]
pub enum ObservablePredicate {
    /// Successful-result observation.
    Result(ResultObservation),
    /// Stable typed-error observation.
    TypedError(TypedErrorObservation),
    /// Initialization, loading, invocation, or shutdown observation.
    Lifecycle(LifecycleObservation),
    /// Content, ownership, path, or preservation observation.
    Filesystem(FilesystemObservation),
    /// Public query-surface observation.
    Query(QueryObservation),
    /// Public schema or generated metadata observation.
    Metadata(MetadataObservation),
    /// Omission, explicit-value, null, or default observation.
    Omission(OmissionObservation),
    /// Call, workspace, dependency, or cancellation isolation observation.
    Isolation(IsolationObservation),
    /// Target, configuration, legacy, or immutable-reference observation.
    Compatibility(CompatibilityObservation),
}

/// Whether an assertion originates in the selected applicability scope or a fixed child case.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum AssertionOrigin {
    /// Assertion selected directly by the reviewed applicability decision set.
    Applicability,
    /// Assertion required by a closed fixed case inventory.
    FixedCase {
        /// Sole family permitted to execute the fixed assertion.
        family: AssertionFamily,
    },
}

/// One reviewed assertion before duplicate merging and authority admission.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConformanceAssertion {
    /// Stable assertion identity used by fixtures and reverse indexes.
    pub id: AssertionId,
    /// Reviewed source of the assertion inventory entry.
    pub origin: AssertionOrigin,
    /// Exact immutable authority coordinates supporting the predicate.
    pub authority_anchors: CanonicalSet<AuthorityAnchor>,
    /// Authority content identities which invalidate the assertion when changed.
    pub source_fingerprints: CanonicalSet<Digest>,
    /// Complete capability set jointly supported by this assertion.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Semantic fixture context; equality is required before assertions may merge.
    pub fixture_context: FixtureContextId,
    /// Closed Rust-observable result contract.
    pub predicate: ObservablePredicate,
    /// Reviewed idiomatic-equivalence rationale when mechanism parity is inappropriate.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub equivalence_decision: Option<SourceLocator>,
    /// Exact execution families allowed to observe the predicate.
    pub permitted_families: CanonicalSet<AssertionFamily>,
}

/// Canonical authored assertion catalog input.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AssertionCatalogInput {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target shared by every assertion.
    pub target_digest: TargetDigest,
    /// Applicability scope identity against which authority drift is checked.
    pub scope_digest: Digest,
    /// Authored rows retained as a list so duplicates remain observable.
    pub assertions: Vec<ConformanceAssertion>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize)]
struct AssertionSemanticKey {
    fixture_context: FixtureContextId,
    predicate: ObservablePredicate,
}

/// Deterministic authority-to-assertion drift categories.
#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AssertionCatalogDrift {
    /// Required assertion identities absent from the authored catalog.
    pub added: CanonicalSet<AssertionId>,
    /// Authored applicability assertions no longer selected by authority review.
    pub removed: CanonicalSet<AssertionId>,
    /// Assertions whose capability membership changed.
    pub reclassified: CanonicalSet<AssertionId>,
    /// Assertions whose authored capability set widened.
    pub merged: CanonicalSet<AssertionId>,
    /// Assertion identities authored more than once before compatible merging.
    pub split: CanonicalSet<AssertionId>,
    /// Assertions whose anchor or authority content identity changed.
    pub fingerprint_changed: CanonicalSet<AssertionId>,
}

impl AssertionCatalogDrift {
    /// Returns true only when selected authority routes and reviewed assertions still agree.
    pub fn is_empty(&self) -> bool {
        self.added.is_empty()
            && self.removed.is_empty()
            && self.reclassified.is_empty()
            && self.merged.is_empty()
            && self.split.is_empty()
            && self.fingerprint_changed.is_empty()
    }

    /// Renders stable category and assertion identities without authority prose.
    pub fn render(&self) -> String {
        let mut lines = Vec::new();
        for (category, ids) in [
            ("added", &self.added),
            ("removed", &self.removed),
            ("reclassified", &self.reclassified),
            ("merged", &self.merged),
            ("split", &self.split),
            ("fingerprint-changed", &self.fingerprint_changed),
        ] {
            lines.extend(ids.iter().map(|id| format!("{category}:{}", id.as_str())));
        }
        lines.join("\n")
    }
}

/// Admitted assertion catalog with canonical private indexes.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AssertionCatalog {
    target_digest: TargetDigest,
    scope_digest: Digest,
    assertions: BTreeMap<AssertionId, ConformanceAssertion>,
    semantic_groups: BTreeMap<AssertionSemanticKey, CanonicalSet<AssertionId>>,
    digest: Digest,
}

impl AssertionCatalog {
    /// Returns the exact target shared by every assertion.
    pub fn target_digest(&self) -> &TargetDigest {
        &self.target_digest
    }

    /// Returns the applicability scope identity used during compilation.
    pub fn scope_digest(&self) -> &Digest {
        &self.scope_digest
    }

    /// Borrows assertions in stable identity order.
    pub fn assertions(&self) -> &BTreeMap<AssertionId, ConformanceAssertion> {
        &self.assertions
    }

    /// Returns the domain-separated complete assertion identity.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }

    /// Returns assertion identities grouped by exact normalized predicate and fixture context.
    pub fn equivalent_assertions(
        &self,
        fixture_context: &FixtureContextId,
        predicate: &ObservablePredicate,
    ) -> Option<&CanonicalSet<AssertionId>> {
        self.semantic_groups.get(&AssertionSemanticKey {
            fixture_context: fixture_context.clone(),
            predicate: predicate.clone(),
        })
    }
}

/// Reports exact changes between current applicability routes and authored assertions.
pub fn assertion_catalog_drift(
    scope: &ConformanceScope,
    input: &AssertionCatalogInput,
) -> AssertionCatalogDrift {
    let expected = scope
        .assertion_capabilities()
        .keys()
        .cloned()
        .collect::<BTreeSet<_>>();
    let applicability = input
        .assertions
        .iter()
        .filter(|assertion| assertion.origin == AssertionOrigin::Applicability)
        .collect::<Vec<_>>();
    let observed = applicability
        .iter()
        .map(|assertion| assertion.id.clone())
        .collect::<BTreeSet<_>>();
    let mut occurrences = BTreeMap::<AssertionId, Vec<&ConformanceAssertion>>::new();
    for assertion in &applicability {
        occurrences
            .entry(assertion.id.clone())
            .or_default()
            .push(assertion);
    }

    let mut reclassified = Vec::new();
    let mut merged = Vec::new();
    let mut split = Vec::new();
    let mut fingerprint_changed = Vec::new();
    for assertion_id in expected.intersection(&observed) {
        let rows = occurrences
            .get(assertion_id)
            .expect("observed assertion has at least one row");
        let observed_capabilities = CanonicalSet::new(
            rows.iter()
                .flat_map(|row| row.capability_ids.iter().cloned()),
        );
        let expected_capabilities = scope
            .assertion_capabilities()
            .get(assertion_id)
            .expect("expected assertion has capabilities");
        if &observed_capabilities != expected_capabilities {
            reclassified.push(assertion_id.clone());
            if observed_capabilities.len() > expected_capabilities.len() {
                merged.push(assertion_id.clone());
            }
        }
        if rows.len() > 1 {
            split.push(assertion_id.clone());
        }
        let (anchors, fingerprints) = expected_authority(scope, assertion_id);
        let observed_anchors = CanonicalSet::new(
            rows.iter()
                .flat_map(|row| row.authority_anchors.iter().cloned()),
        );
        let observed_fingerprints = CanonicalSet::new(
            rows.iter()
                .flat_map(|row| row.source_fingerprints.iter().cloned()),
        );
        if observed_anchors != anchors || observed_fingerprints != fingerprints {
            fingerprint_changed.push(assertion_id.clone());
        }
    }

    AssertionCatalogDrift {
        added: CanonicalSet::new(expected.difference(&observed).cloned()),
        removed: CanonicalSet::new(observed.difference(&expected).cloned()),
        reclassified: CanonicalSet::new(reclassified),
        merged: CanonicalSet::new(merged),
        split: CanonicalSet::new(split),
        fingerprint_changed: CanonicalSet::new(fingerprint_changed),
    }
}

/// Compiles assertions into a deterministic catalog after total authority validation.
pub fn compile_assertion_catalog(
    scope: &ConformanceScope,
    input: AssertionCatalogInput,
) -> Result<AssertionCatalog, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    if input.target_digest != *scope.target_digest() || input.scope_digest != *scope.digest() {
        diagnostics.push(assertion_diagnostic(
            None,
            "assertion catalog target or scope identity is stale",
        ));
    }
    let drift = assertion_catalog_drift(scope, &input);
    for assertion_id in drift
        .added
        .iter()
        .chain(drift.removed.iter())
        .chain(drift.reclassified.iter())
        .chain(drift.fingerprint_changed.iter())
    {
        diagnostics.push(assertion_diagnostic(
            Some(assertion_id.clone()),
            "assertion authority scope or fingerprint changed",
        ));
    }

    let mut assertions = BTreeMap::<AssertionId, ConformanceAssertion>::new();
    for assertion in input.assertions {
        validate_assertion_shape(&assertion, &mut diagnostics);
        match assertions.entry(assertion.id.clone()) {
            std::collections::btree_map::Entry::Vacant(entry) => {
                entry.insert(assertion);
            }
            std::collections::btree_map::Entry::Occupied(mut entry) => {
                if !merge_compatible(entry.get(), &assertion) {
                    diagnostics.push(assertion_diagnostic(
                        Some(assertion.id),
                        "duplicate assertion has conflicting predicate or fixture context",
                    ));
                    continue;
                }
                let retained = entry.get_mut();
                retained.authority_anchors = CanonicalSet::new(
                    retained
                        .authority_anchors
                        .iter()
                        .cloned()
                        .chain(assertion.authority_anchors),
                );
                retained.source_fingerprints = CanonicalSet::new(
                    retained
                        .source_fingerprints
                        .iter()
                        .cloned()
                        .chain(assertion.source_fingerprints),
                );
                retained.capability_ids = CanonicalSet::new(
                    retained
                        .capability_ids
                        .iter()
                        .cloned()
                        .chain(assertion.capability_ids),
                );
            }
        }
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }

    let mut groups = BTreeMap::<AssertionSemanticKey, Vec<AssertionId>>::new();
    for assertion in assertions.values() {
        groups
            .entry(AssertionSemanticKey {
                fixture_context: assertion.fixture_context.clone(),
                predicate: assertion.predicate.clone(),
            })
            .or_default()
            .push(assertion.id.clone());
    }
    let semantic_groups = groups
        .into_iter()
        .map(|(key, ids)| (key, CanonicalSet::new(ids)))
        .collect::<BTreeMap<_, _>>();
    let encoded_semantic_groups = semantic_groups.iter().collect::<Vec<_>>();
    let digest = canonical_digest(
        DigestDomain::ConformanceAssertionCatalog,
        &(
            &input.target_digest,
            &input.scope_digest,
            &assertions,
            &encoded_semantic_groups,
        ),
    )
    .map_err(|_| {
        one_assertion_diagnostic(None, "assertion catalog cannot be encoded canonically")
    })?;
    Ok(AssertionCatalog {
        target_digest: input.target_digest,
        scope_digest: input.scope_digest,
        assertions,
        semantic_groups,
        digest,
    })
}

fn expected_authority(
    scope: &ConformanceScope,
    assertion_id: &AssertionId,
) -> (CanonicalSet<AuthorityAnchor>, CanonicalSet<Digest>) {
    let records = scope.existing_records().values().filter(|record| {
        record
            .assertion_ids
            .iter()
            .any(|candidate| candidate == assertion_id)
    });
    let records = records.collect::<Vec<_>>();
    (
        CanonicalSet::new(records.iter().map(|record| record.authority_anchor.clone())),
        CanonicalSet::new(
            records
                .iter()
                .map(|record| record.source_fingerprint.clone()),
        ),
    )
}

fn validate_assertion_shape(
    assertion: &ConformanceAssertion,
    diagnostics: &mut Vec<ConformanceDiagnostic>,
) {
    let valid_origin = match &assertion.origin {
        AssertionOrigin::Applicability => assertion.id.as_str().starts_with("assertion/"),
        AssertionOrigin::FixedCase { family } => {
            assertion.id.as_str().starts_with("assertion/fixed/")
                && assertion.permitted_families.contains(family)
        }
    };
    if !valid_origin
        || assertion.authority_anchors.is_empty()
        || assertion.source_fingerprints.is_empty()
        || assertion.capability_ids.is_empty()
        || assertion.permitted_families.is_empty()
    {
        diagnostics.push(assertion_diagnostic(
            Some(assertion.id.clone()),
            "assertion is orphaned or lacks authority predicate fixture or family scope",
        ));
    }
    let requires_equivalence = assertion
        .id
        .as_str()
        .starts_with("assertion/definitive-go-client/");
    if requires_equivalence != assertion.equivalence_decision.is_some() {
        diagnostics.push(assertion_diagnostic(
            Some(assertion.id.clone()),
            "assertion idiomatic decision identity is missing or unexpected",
        ));
    }
}

fn merge_compatible(left: &ConformanceAssertion, right: &ConformanceAssertion) -> bool {
    left.origin == right.origin
        && left.fixture_context == right.fixture_context
        && left.predicate == right.predicate
        && left.equivalence_decision == right.equivalence_decision
        && left.permitted_families == right.permitted_families
}

fn assertion_diagnostic(
    assertion_id: Option<AssertionId>,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::ConformanceAssertionInvalid,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Catalog),
            assertion_id,
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn one_assertion_diagnostic(
    assertion_id: Option<AssertionId>,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([assertion_diagnostic(assertion_id, detail)])
        .expect("one assertion diagnostic is non-empty")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_predicate_variant_has_a_stable_canonical_shape() {
        let predicates = [
            ObservablePredicate::Result(ResultObservation::ExactValue),
            ObservablePredicate::TypedError(TypedErrorObservation::Fields),
            ObservablePredicate::Lifecycle(LifecycleObservation::CloseAndReap),
            ObservablePredicate::Filesystem(FilesystemObservation::Preservation),
            ObservablePredicate::Query(QueryObservation::Core),
            ObservablePredicate::Metadata(MetadataObservation::Definition),
            ObservablePredicate::Omission(OmissionObservation::Omitted),
            ObservablePredicate::Isolation(IsolationObservation::Workspace),
            ObservablePredicate::Compatibility(CompatibilityObservation::TargetVersion),
        ];
        let values = predicates
            .iter()
            .map(|predicate| serde_json::to_string(predicate).unwrap())
            .collect::<BTreeSet<_>>();
        assert_eq!(values.len(), predicates.len());
    }
}
