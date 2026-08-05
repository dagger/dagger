//! Authority registry, source-bundle, and source-item coverage validation.
//!
//! Authority metadata is validated against an already validated target before any filesystem
//! adapter may open source paths. Exact source bytes then enter this module as in-memory bundles;
//! digesting, include expansion, exclusion checks, and coverage resolution remain pure and
//! deterministic. This split keeps extractors unable to widen their own filesystem authority.

use std::collections::{BTreeMap, BTreeSet};
use std::ops::Deref;

use serde::Serialize;

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, Validation,
};
use crate::model::{
    AuthorityClass, AuthorityId, AuthorityRegistry, AuthoritySource, CanonicalSet, CapabilityId,
    CommitSha, Digest, ExtractorIdentity, RepositoryId, RepositoryRelativePath, SourceExclusion,
    SourceItemId, SourceItemInventory, SourceSelector, TargetDescriptor,
};
use crate::target::ValidatedTargetDescriptor;

const AUTHORITY_CLASSES: [AuthorityClass; 7] = [
    AuthorityClass::EngineSchema,
    AuthorityClass::GoClient,
    AuthorityClass::GoEngineSdk,
    AuthorityClass::GoCodegen,
    AuthorityClass::GoIntegrationTests,
    AuthorityClass::SdkContractHarness,
    AuthorityClass::RustPolicy,
];

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Exact bytes selected from one authority repository, keyed by repository-relative path.
///
/// A bundle has no filesystem handle or repository root. Once constructed by a loader, extractors
/// can inspect only these bytes and cannot discover adjacent files.
pub struct SourceBundle {
    files: BTreeMap<RepositoryRelativePath, Vec<u8>>,
}

impl SourceBundle {
    /// Constructs a bundle while normalizing input enumeration by path.
    pub fn new(files: impl IntoIterator<Item = (RepositoryRelativePath, Vec<u8>)>) -> Self {
        Self {
            files: files.into_iter().collect(),
        }
    }

    /// Borrows the exact selected file bytes in canonical path order.
    pub fn files(&self) -> &BTreeMap<RepositoryRelativePath, Vec<u8>> {
        &self.files
    }

    pub(crate) fn insert(&mut self, path: RepositoryRelativePath, bytes: Vec<u8>) {
        self.files.insert(path, bytes);
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Per-authority source bundles supplied to pure registry validation and extractors.
pub struct AuthoritySourceBundles {
    bundles: BTreeMap<AuthorityId, SourceBundle>,
}

impl AuthoritySourceBundles {
    /// Constructs an authority-keyed bundle set in stable key order.
    pub fn new(bundles: impl IntoIterator<Item = (AuthorityId, SourceBundle)>) -> Self {
        Self {
            bundles: bundles.into_iter().collect(),
        }
    }

    /// Borrows all bundles in authority-ID order.
    pub fn bundles(&self) -> &BTreeMap<AuthorityId, SourceBundle> {
        &self.bundles
    }

    pub(crate) fn insert(&mut self, authority_id: AuthorityId, bundle: SourceBundle) {
        self.bundles.insert(authority_id, bundle);
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Registry whose classes, identities, repositories, revisions, and selections match a target.
///
/// The validated target is retained so later adapters cannot detach the registry from the target
/// that authorized its repository selections.
pub struct ValidatedAuthorityRegistry {
    target: TargetDescriptor,
    registry: AuthorityRegistry,
}

impl ValidatedAuthorityRegistry {
    /// Borrows the target that authorized this registry.
    pub fn target(&self) -> &TargetDescriptor {
        &self.target
    }

    /// Returns the validated durable registry.
    pub fn into_inner(self) -> AuthorityRegistry {
        self.registry
    }
}

impl Deref for ValidatedAuthorityRegistry {
    type Target = AuthorityRegistry;

    fn deref(&self) -> &Self::Target {
        &self.registry
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Registry plus exact source bytes whose selections and content digests were validated.
pub struct ValidatedAuthoritySources {
    registry: ValidatedAuthorityRegistry,
    bundles: AuthoritySourceBundles,
    expanded_paths: BTreeMap<AuthorityId, CanonicalSet<RepositoryRelativePath>>,
}

impl ValidatedAuthoritySources {
    /// Borrows the structurally validated registry.
    pub fn registry(&self) -> &ValidatedAuthorityRegistry {
        &self.registry
    }

    /// Borrows the exact bytes available to each authority extractor.
    pub fn bundles(&self) -> &AuthoritySourceBundles {
        &self.bundles
    }

    /// Borrows the normalized files selected by each authority's include set.
    pub fn expanded_paths(&self) -> &BTreeMap<AuthorityId, CanonicalSet<RepositoryRelativePath>> {
        &self.expanded_paths
    }
}

/// Validates the complete authority registry against an immutable target.
///
/// This phase performs no I/O. A successful value is the capability token required by the local
/// source loader, ensuring path opening cannot precede target and selection validation.
pub fn validate_authority_registry(
    target: &ValidatedTargetDescriptor,
    registry: AuthorityRegistry,
) -> Validation<ValidatedAuthorityRegistry> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut seen_ids = BTreeSet::new();
    let mut class_counts = BTreeMap::<AuthorityClass, usize>::new();

    for (registry_id, source) in &registry.authorities {
        if registry_id != &source.authority_id || !seen_ids.insert(source.authority_id.clone()) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityDuplicate,
                source.authority_id.to_string(),
                None,
                "authority map keys and embedded IDs must form one unique identity set",
            ));
        }

        *class_counts
            .entry(source.authority_class.clone())
            .or_default() += 1;
        let (expected_repository, expected_revision) = expected_authority_identity(target, source);

        if &source.repository != expected_repository {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityRepositoryInvalid,
                source.authority_id.to_string(),
                None,
                format!(
                    "{} authority must use repository {expected_repository}",
                    authority_class_name(&source.authority_class)
                ),
            ));
        }
        if &source.revision != expected_revision {
            diagnostics.push(ContractDiagnostic::new(
                revision_mismatch_code(&source.authority_class),
                source.authority_id.to_string(),
                None,
                format!(
                    "{} authority revision must match {expected_revision}",
                    authority_class_name(&source.authority_class)
                ),
            ));
        }
        if source.include.is_empty() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthoritySourceEmpty,
                source.authority_id.to_string(),
                None,
                "authority include set must not be empty",
            ));
        }

        for exclusion in source.exclude.as_slice() {
            if !source
                .include
                .as_slice()
                .iter()
                .any(|include| selector_contains(include, &exclusion.selector))
            {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::AuthorityExclusionInvalid,
                    source.authority_id.to_string(),
                    None,
                    "exclusion must identify an exact path or symbol inside the include set",
                ));
            }
        }
    }

    for authority_class in AUTHORITY_CLASSES {
        let count = class_counts
            .get(&authority_class)
            .copied()
            .unwrap_or_default();
        if count != 1 {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityClassInvalid,
                authority_class_name(&authority_class),
                None,
                format!("authority class must resolve exactly once, observed {count}"),
            ));
        }
    }

    diagnostics.finish(ValidatedAuthorityRegistry {
        target: target.clone().into_inner(),
        registry,
    })
}

/// Recomputes one authority's digest from normalized selected paths and exact file bytes.
///
/// File enumeration and overlapping selectors cannot affect the result: selected paths are
/// deduplicated in a `BTreeSet`, and each content identity is paired with its canonical path.
pub fn recompute_source_digest(
    source: &AuthoritySource,
    bundle: &SourceBundle,
) -> Validation<Digest> {
    let mut diagnostics = DiagnosticCollector::default();
    if source.include.is_empty() {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::AuthoritySourceEmpty,
            source.authority_id.to_string(),
            None,
            "authority include set must not be empty",
        ));
    }
    let expanded_paths = expand_includes(source, bundle, &mut diagnostics);
    validate_exclusions(source, &expanded_paths, &mut diagnostics);

    #[derive(Serialize)]
    struct SourceFileIdentity<'a> {
        path: &'a RepositoryRelativePath,
        content_digest: Digest,
    }

    #[derive(Serialize)]
    struct NormalizedSourceIdentity<'a> {
        include: &'a CanonicalSet<SourceSelector>,
        exclude: &'a CanonicalSet<SourceExclusion>,
        extractor: &'a ExtractorIdentity,
        files: Vec<SourceFileIdentity<'a>>,
    }

    let files = expanded_paths
        .iter()
        .filter_map(|path| {
            bundle.files.get(path).map(|bytes| SourceFileIdentity {
                path,
                content_digest: Digest::sha256(bytes),
            })
        })
        .collect::<Vec<_>>();
    // Bind the reviewed selection and extractor as well as the selected bytes. Otherwise changing
    // a symbol boundary or extraction version over unchanged files could retain the old digest.
    let identity = NormalizedSourceIdentity {
        include: &source.include,
        exclude: &source.exclude,
        extractor: &source.extractor,
        files,
    };
    let digest = canonical_digest(DigestDomain::Source, &identity).map_err(|_| {
        DiagnosticSet::new([ContractDiagnostic::new(
            DiagnosticCode::AuthorityDrift,
            source.authority_id.to_string(),
            None,
            "normalized source identity could not be encoded",
        )])
        .expect("one diagnostic always forms a non-empty set")
    })?;

    diagnostics.finish(digest)
}

/// Validates include expansion, exact exclusions, and content digests for every authority.
pub fn validate_authority_sources(
    registry: ValidatedAuthorityRegistry,
    bundles: AuthoritySourceBundles,
) -> Validation<ValidatedAuthoritySources> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut expanded_paths = BTreeMap::new();

    for (authority_id, source) in &registry.authorities {
        let Some(bundle) = bundles.bundles.get(authority_id) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthoritySourceEmpty,
                authority_id.to_string(),
                None,
                "validated authority has no loaded source bundle",
            ));
            continue;
        };

        let mut source_diagnostics = DiagnosticCollector::default();
        let selected = expand_includes(source, bundle, &mut source_diagnostics);
        validate_exclusions(source, &selected, &mut source_diagnostics);
        match source_diagnostics.finish(()) {
            Ok(()) => {
                match recompute_source_digest(source, bundle) {
                    Ok(observed) if observed == source.source_digest => {}
                    Ok(_) => diagnostics.push(ContractDiagnostic::new(
                        DiagnosticCode::AuthorityDrift,
                        authority_id.to_string(),
                        None,
                        "recorded source digest differs from normalized selected bytes",
                    )),
                    Err(errors) => diagnostics.extend(errors.into_inner()),
                }
                expanded_paths.insert(authority_id.clone(), CanonicalSet::new(selected));
            }
            Err(errors) => diagnostics.extend(errors.into_inner()),
        }
    }

    for authority_id in bundles.bundles.keys() {
        if !registry.authorities.contains_key(authority_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityDuplicate,
                authority_id.to_string(),
                None,
                "source bundle does not belong to a registered authority",
            ));
        }
    }

    diagnostics.finish(ValidatedAuthoritySources {
        registry,
        bundles,
        expanded_paths,
    })
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// How one extracted source item is exhaustively accounted for.
pub enum SourceItemDisposition {
    /// The item defines one or more atomic capabilities.
    Primary(CanonicalSet<CapabilityId>),
    /// The item corroborates one or more capabilities defined by another primary item.
    Reference(CanonicalSet<CapabilityId>),
    /// The item is omitted only through this exact reviewed authority exclusion.
    Excluded(SourceExclusion),
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// One source item's selection provenance and exclusive coverage disposition.
pub struct SourceItemCoverage {
    /// Stable identity of the extracted item being accounted for.
    pub source_item_id: SourceItemId,
    /// Exact registered selector through which the extractor received the item.
    pub selected_by: SourceSelector,
    /// The item's one exclusive role in exhaustive coverage.
    pub disposition: SourceItemDisposition,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Exhaustive source-item coverage assignments keyed by source-item identity.
pub struct SourceCoverage {
    /// Assignments keyed by the same source-item identity embedded in each value.
    pub items: BTreeMap<SourceItemId, SourceItemCoverage>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Source coverage proven exhaustive with no stale reviewed exclusion.
pub struct ValidatedSourceCoverage(SourceCoverage);

impl ValidatedSourceCoverage {
    /// Borrows the validated coverage assignments.
    pub fn as_inner(&self) -> &SourceCoverage {
        &self.0
    }

    /// Returns the validated coverage assignments.
    pub fn into_inner(self) -> SourceCoverage {
        self.0
    }
}

/// Validates uniform coverage for active, deprecated, skipped, removed, and harness-self items.
///
/// Lifecycle state intentionally does not alter this accounting rule: every selected item must be
/// primary, a reference, or attached to the exact exclusion that reviewed its omission.
pub fn validate_source_coverage(
    sources: &ValidatedAuthoritySources,
    inventory: &SourceItemInventory,
    coverage: SourceCoverage,
) -> Validation<ValidatedSourceCoverage> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut used_exclusions = BTreeSet::<(AuthorityId, SourceExclusion)>::new();

    for (source_item_id, item) in &inventory.items {
        let Some(assignment) = coverage.items.get(source_item_id) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                source_item_id.to_string(),
                Some(item.locator.clone()),
                "selected source item has no coverage disposition",
            ));
            continue;
        };

        if &assignment.source_item_id != source_item_id {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityDuplicate,
                source_item_id.to_string(),
                Some(item.locator.clone()),
                "coverage map key differs from its embedded source-item identity",
            ));
        }

        let Some(authority) = sources.registry.authorities.get(&item.authority_id) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityAuthorityMissing,
                source_item_id.to_string(),
                Some(item.locator.clone()),
                "source item references an unregistered authority",
            ));
            continue;
        };
        if !authority
            .include
            .as_slice()
            .iter()
            .any(|include| selector_contains(include, &assignment.selected_by))
        {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                source_item_id.to_string(),
                Some(item.locator.clone()),
                "source item selection is outside its authority include set",
            ));
        }

        match &assignment.disposition {
            SourceItemDisposition::Primary(capability_ids)
            | SourceItemDisposition::Reference(capability_ids) => {
                if capability_ids.is_empty() {
                    diagnostics.push(ContractDiagnostic::new(
                        DiagnosticCode::CapabilitySourceMissing,
                        source_item_id.to_string(),
                        Some(item.locator.clone()),
                        "primary and reference coverage require at least one capability",
                    ));
                }
                if authority.exclude.as_slice().iter().any(|exclusion| {
                    selector_contains(&exclusion.selector, &assignment.selected_by)
                }) {
                    diagnostics.push(ContractDiagnostic::new(
                        DiagnosticCode::AuthorityExclusionInvalid,
                        source_item_id.to_string(),
                        Some(item.locator.clone()),
                        "an excluded selection cannot simultaneously define or reference a capability",
                    ));
                }
            }
            SourceItemDisposition::Excluded(exclusion) => {
                let is_exact = exclusion.selector == assignment.selected_by
                    && authority.exclude.as_slice().contains(exclusion);
                if is_exact {
                    used_exclusions.insert((item.authority_id.clone(), exclusion.clone()));
                } else {
                    diagnostics.push(ContractDiagnostic::new(
                        DiagnosticCode::AuthorityExclusionInvalid,
                        source_item_id.to_string(),
                        Some(item.locator.clone()),
                        "source item exclusion must exactly match a reviewed registry entry",
                    ));
                }
            }
        }
    }

    for (source_item_id, assignment) in &coverage.items {
        if !inventory.items.contains_key(source_item_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                assignment.source_item_id.to_string(),
                None,
                "coverage assignment references no extracted source item",
            ));
        }
    }

    for (authority_id, authority) in &sources.registry.authorities {
        for exclusion in authority.exclude.as_slice() {
            if !used_exclusions.contains(&(authority_id.clone(), exclusion.clone())) {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::AuthorityExclusionInvalid,
                    authority_id.to_string(),
                    None,
                    "reviewed exclusion is stale because it covers no extracted source item",
                ));
            }
        }
    }

    diagnostics.finish(ValidatedSourceCoverage(coverage))
}

fn expected_authority_identity<'a>(
    target: &'a TargetDescriptor,
    source: &AuthoritySource,
) -> (&'a RepositoryId, &'a CommitSha) {
    match source.authority_class {
        AuthorityClass::GoClient => (&target.go_sdk_repository, &target.go_sdk_revision),
        AuthorityClass::SdkContractHarness => (
            &target.sdk_contract_repository,
            &target.sdk_contract_revision,
        ),
        AuthorityClass::EngineSchema
        | AuthorityClass::GoEngineSdk
        | AuthorityClass::GoCodegen
        | AuthorityClass::GoIntegrationTests
        | AuthorityClass::RustPolicy => (&target.dagger_repository, &target.dagger_revision),
    }
}

fn revision_mismatch_code(authority_class: &AuthorityClass) -> DiagnosticCode {
    match authority_class {
        AuthorityClass::SdkContractHarness => DiagnosticCode::SdkContractRevisionMismatch,
        _ => DiagnosticCode::AuthorityRevisionMismatch,
    }
}

fn authority_class_name(authority_class: &AuthorityClass) -> &'static str {
    match authority_class {
        AuthorityClass::EngineSchema => "engine-schema",
        AuthorityClass::GoClient => "go-client",
        AuthorityClass::GoEngineSdk => "go-engine-sdk",
        AuthorityClass::GoCodegen => "go-codegen",
        AuthorityClass::GoIntegrationTests => "go-integration-tests",
        AuthorityClass::SdkContractHarness => "sdk-contract-harness",
        AuthorityClass::RustPolicy => "rust-policy",
    }
}

pub(crate) fn selector_path(selector: &SourceSelector) -> &RepositoryRelativePath {
    match selector {
        SourceSelector::Path(selector) => &selector.path,
        SourceSelector::Symbol(selector) => &selector.path,
    }
}

fn selector_contains(container: &SourceSelector, candidate: &SourceSelector) -> bool {
    match (container, candidate) {
        (SourceSelector::Path(container), candidate) => {
            path_contains(&container.path, selector_path(candidate))
        }
        (SourceSelector::Symbol(container), SourceSelector::Symbol(candidate)) => {
            container == candidate
        }
        (SourceSelector::Symbol(_), SourceSelector::Path(_)) => false,
    }
}

fn path_contains(container: &RepositoryRelativePath, candidate: &RepositoryRelativePath) -> bool {
    candidate == container
        || candidate
            .as_str()
            .strip_prefix(container.as_str())
            .is_some_and(|suffix| suffix.starts_with('/'))
}

fn expand_includes(
    source: &AuthoritySource,
    bundle: &SourceBundle,
    diagnostics: &mut DiagnosticCollector,
) -> BTreeSet<RepositoryRelativePath> {
    let mut expanded = BTreeSet::new();
    for selector in source.include.as_slice() {
        let matched = bundle
            .files
            .keys()
            .filter(|path| match selector {
                SourceSelector::Path(selector) => path_contains(&selector.path, path),
                SourceSelector::Symbol(selector) => path == &&selector.path,
            })
            .cloned()
            .collect::<Vec<_>>();
        if matched.is_empty() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthoritySourceEmpty,
                source.authority_id.to_string(),
                None,
                format!(
                    "include path {} selected no source file",
                    selector_path(selector)
                ),
            ));
        }
        expanded.extend(matched);
    }
    expanded
}

fn validate_exclusions(
    source: &AuthoritySource,
    expanded: &BTreeSet<RepositoryRelativePath>,
    diagnostics: &mut DiagnosticCollector,
) {
    for exclusion in source.exclude.as_slice() {
        let resolves = match &exclusion.selector {
            SourceSelector::Path(selector) => expanded
                .iter()
                .any(|path| path_contains(&selector.path, path)),
            SourceSelector::Symbol(selector) => expanded.contains(&selector.path),
        };
        if !resolves {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityExclusionInvalid,
                source.authority_id.to_string(),
                None,
                format!(
                    "exclusion path {} resolves no selected source item",
                    selector_path(&exclusion.selector)
                ),
            ));
        }
    }
}
