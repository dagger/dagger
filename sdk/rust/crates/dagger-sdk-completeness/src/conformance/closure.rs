//! Engine-free implementation-closure adapters and bundle admission.
//!
//! The bundle consumes one current closure for each completed Rust SDK domain. It records legacy
//! evidence format differences explicitly, admits reviewed asset compatibility without pretending
//! an older commit is current, and rejects replay or engine work before artifact construction.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{CanonicalSet, Digest, TargetDigest};

use super::{
    ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, SubjectIdentity,
};

/// Exact six-domain implementation closure consumed by umbrella sign-off.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ChildClosure {
    /// Stable connector, session transport, telemetry, and shutdown closure.
    Transport,
    /// Public client lifecycle and resource-management closure.
    ClientLifecycle,
    /// Generated Core schema and binding closure.
    CoreCodegen,
    /// Engine hook, package, operation, and runtime-construction closure.
    EngineIntegration,
    /// Rust-authored module compiler and dispatch closure.
    ModuleAuthoring,
    /// Standalone generated-client compiler and query closure.
    StandaloneClient,
}

/// Current durable evidence shape adapted for each completed child domain.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ChildEvidenceFormat {
    /// Transport observation registry format.
    TransportObservationRegistry,
    /// Client lifecycle evidence registry format.
    ClientEvidenceRegistry,
    /// Core code-generation evidence registry format.
    CoreCodegenEvidenceRegistry,
    /// Current engine-free engine-integration closure format.
    EngineIntegrationImplementationClosure,
    /// Historical exact-engine result, retained only so misclassification fails explicitly.
    EngineIntegrationHistoricalSignoff,
    /// Module-authoring engine-free closure format.
    ModuleAuthoringImplementationClosure,
    /// Standalone-client engine-free closure format.
    ClientGenerationClosure,
}

/// Stable outcome retained by every child evidence adapter.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClosureOutcome {
    /// All owned closure gates passed.
    Passed,
    /// At least one owned closure gate failed.
    Failed,
    /// Required closure work did not execute.
    Skipped,
}

/// Generated content whose exact digest must be carried into sign-off.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum GeneratedAssetDomain {
    /// Checked generated Core bindings.
    CoreBindings,
    /// Engine-packaged Rust SDK runtime and manifest content.
    EnginePackage,
    /// Checked generated module descriptors and Rust assets.
    ModuleAssets,
    /// Checked generated standalone-client project content.
    StandaloneClientAssets,
}

/// A child either matches the implementation subject or a separately reviewed compatible asset.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "kind")]
pub enum ClosureSubjectBinding {
    /// Evidence was produced directly for the current implementation identity.
    Subject {
        /// Exact subject revision or source digest.
        identity: SubjectIdentity,
    },
    /// Evidence was produced for an immutable asset with a separate compatibility decision.
    ReviewedAsset {
        /// Exact older or prebuilt asset identity.
        asset_digest: Digest,
    },
}

/// Explicit compatibility decision for one immutable older asset.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AssetCompatibility {
    /// Immutable asset whose evidence is proposed for reuse.
    pub asset_digest: Digest,
    /// Current subject proven compatible with the asset.
    pub compatible_subject: SubjectIdentity,
    /// Digest of the reviewed compatibility inputs and decision.
    pub compatibility_input_digest: Digest,
}

/// Typed action vocabulary used to prove that closure admission consumes rather than replays.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case", tag = "action", content = "domain")]
pub enum ClosurePlanAction {
    /// Consume one child closure without executing its gates again.
    ConsumeChild(ChildClosure),
    /// Consume one checked generated-asset identity.
    ConsumeGeneratedAsset(GeneratedAssetDomain),
    /// Consume the complete native-platform matrix identity.
    ConsumePlatformMatrix,
    /// Consume the ordinary Rust security and hygiene identity.
    ConsumeRustSecurity,
    /// Forbidden replay of Rust unit tests.
    ReplayRustUnit,
    /// Forbidden replay of Rust fixture tests.
    ReplayRustFixture,
    /// Forbidden replay of formatting checks.
    ReplayFormat,
    /// Forbidden replay of Clippy.
    ReplayClippy,
    /// Forbidden replay of rustdoc checks.
    ReplayRustdoc,
    /// Forbidden replay of Cargo Deny.
    ReplayCargoDeny,
    /// Forbidden replay of the direct Go adapter slice.
    ReplayDirectGo,
    /// Forbidden engine startup before closure admission.
    StartEngine,
}

impl ClosurePlanAction {
    fn is_consumption(&self) -> bool {
        matches!(
            self,
            Self::ConsumeChild(_)
                | Self::ConsumeGeneratedAsset(_)
                | Self::ConsumePlatformMatrix
                | Self::ConsumeRustSecurity
        )
    }
}

/// Normalized adapter input for one existing child evidence format.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LegacyChildClosureEvidence {
    /// Child closure domain claimed by the producer.
    pub child: ChildClosure,
    /// Producer format which fixes the only valid child domain.
    pub evidence_format: ChildEvidenceFormat,
    /// Exact Dagger target assessed by the child.
    pub target_digest: TargetDigest,
    /// Current subject or explicitly reusable immutable asset.
    pub subject_binding: ClosureSubjectBinding,
    /// Canonical identity of the complete child result.
    pub closure_digest: Digest,
    /// True only when the child result contains no engine-backed evidence.
    pub engine_free: bool,
    /// Honest terminal child outcome.
    pub outcome: ClosureOutcome,
    /// Generated assets owned and attested by the child.
    pub generated_assets: BTreeMap<GeneratedAssetDomain, Digest>,
}

/// One normalized child reference safe to include in the umbrella bundle.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ChildClosureReference {
    /// Normalized child closure domain.
    pub child: ChildClosure,
    /// Original evidence shape retained for auditability.
    pub evidence_format: ChildEvidenceFormat,
    /// Exact Dagger target assessed by the child.
    pub target_digest: TargetDigest,
    /// Current subject or explicitly compatible immutable asset.
    pub subject_binding: ClosureSubjectBinding,
    /// Canonical child closure identity.
    pub closure_digest: Digest,
    /// Engine-free boundary, revalidated during bundle admission.
    pub engine_free: bool,
    /// Passed outcome required by bundle admission.
    pub outcome: ClosureOutcome,
    /// Generated assets attested by this child.
    pub generated_assets: BTreeMap<GeneratedAssetDomain, Digest>,
}

/// Complete authored input evaluated before artifact work or engine startup.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ImplementationClosureBundleInput {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target shared by all inputs.
    pub target_digest: TargetDigest,
    /// Current Rust implementation revision or source digest.
    pub subject: SubjectIdentity,
    /// Authored child references retained as a list so duplicates remain observable.
    pub child_closures: Vec<ChildClosureReference>,
    /// Authored immutable-asset decisions retained as a list for duplicate detection.
    pub compatible_assets: Vec<AssetCompatibility>,
    /// Complete cross-child generated-asset identity map.
    pub generated_assets: BTreeMap<GeneratedAssetDomain, Digest>,
    /// Complete native-platform matrix identity.
    pub platform_matrix_digest: Digest,
    /// Ordinary Rust security and hygiene closure identity.
    pub rust_security_digest: Digest,
    /// Proposed consume-only plan; replay and engine variants remain representable to fail closed.
    pub plan: Vec<ClosurePlanAction>,
}

/// Admitted closure identity consumed by exact-target sign-off without replay.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ImplementationClosureBundle {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact Dagger target admitted by every child.
    pub target_digest: TargetDigest,
    /// Current Rust implementation identity.
    pub subject: SubjectIdentity,
    /// Exact six-child map after duplicate and compatibility validation.
    pub child_closures: BTreeMap<ChildClosure, ChildClosureReference>,
    /// Exact compatibility decisions indexed by reusable asset identity.
    pub compatible_assets: BTreeMap<Digest, AssetCompatibility>,
    /// Complete generated-asset identity map.
    pub generated_assets: BTreeMap<GeneratedAssetDomain, Digest>,
    /// Complete native-platform matrix identity.
    pub platform_matrix_digest: Digest,
    /// Ordinary Rust security and hygiene closure identity.
    pub rust_security_digest: Digest,
    /// Canonical consume-only action set.
    pub plan: CanonicalSet<ClosurePlanAction>,
    /// Domain-separated identity of every admitted closure input.
    pub bundle_digest: Digest,
}

/// Checked consume-only closure shape without pretending that release evidence already exists.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ImplementationClosurePlanFixture {
    /// Durable artifact format.
    pub format_version: ConformanceFormatVersion,
    /// Exact current evidence format required for each child slot.
    pub child_formats: BTreeMap<ChildClosure, ChildEvidenceFormat>,
    /// Complete generated-asset domains required before sign-off.
    pub generated_asset_domains: CanonicalSet<GeneratedAssetDomain>,
    /// Whether a complete native-platform matrix identity is mandatory.
    pub requires_platform_matrix: bool,
    /// Whether ordinary Rust security and hygiene identity is mandatory.
    pub requires_rust_security: bool,
    /// Always false: engine-backed historical results cannot become local closure.
    pub permits_historical_engine_signoff: bool,
    /// Exact consume-only action set.
    pub actions: CanonicalSet<ClosurePlanAction>,
    /// Domain-separated identity of the reviewed evidence slots and actions.
    pub plan_digest: Digest,
}

/// Converts one current child evidence shape into the common closure reference.
pub fn adapt_child_closure(
    evidence: LegacyChildClosureEvidence,
) -> Result<ChildClosureReference, ConformanceDiagnosticSet> {
    let expected_child = child_for_format(evidence.evidence_format);
    if expected_child != Some(evidence.child)
        || evidence.outcome != ClosureOutcome::Passed
        || !evidence.engine_free
    {
        let code = if !evidence.engine_free
            || evidence.evidence_format == ChildEvidenceFormat::EngineIntegrationHistoricalSignoff
        {
            ConformanceDiagnosticCode::ImplementationClosureBoundaryInvalid
        } else {
            ConformanceDiagnosticCode::ImplementationClosureIncomplete
        };
        return Err(one_closure_diagnostic(
            code,
            "child closure format outcome or engine-free boundary is invalid",
        ));
    }
    Ok(ChildClosureReference {
        child: evidence.child,
        evidence_format: evidence.evidence_format,
        target_digest: evidence.target_digest,
        subject_binding: evidence.subject_binding,
        closure_digest: evidence.closure_digest,
        engine_free: evidence.engine_free,
        outcome: evidence.outcome,
        generated_assets: evidence.generated_assets,
    })
}

/// Admits exactly six matching closures, generated assets, platform, and security identities.
pub fn assemble_implementation_closure_bundle(
    input: ImplementationClosureBundleInput,
) -> Result<ImplementationClosureBundle, ConformanceDiagnosticSet> {
    let mut diagnostics = Vec::new();
    let mut children = BTreeMap::new();
    for child in input.child_closures {
        if child.target_digest != input.target_digest
            || child.outcome != ClosureOutcome::Passed
            || !child.engine_free
            || child_for_format(child.evidence_format) != Some(child.child)
        {
            diagnostics.push(closure_diagnostic(
                ConformanceDiagnosticCode::ImplementationClosureIncomplete,
                "child closure is stale failed skipped or format-incompatible",
            ));
        }
        if children.insert(child.child, child).is_some() {
            diagnostics.push(closure_diagnostic(
                ConformanceDiagnosticCode::ImplementationClosureIncomplete,
                "child closure identity is duplicated",
            ));
        }
    }
    if children.keys().copied().collect::<BTreeSet<_>>() != required_child_closures() {
        diagnostics.push(closure_diagnostic(
            ConformanceDiagnosticCode::ImplementationClosureIncomplete,
            "child closure set is incomplete or unknown",
        ));
    }

    let mut compatible_assets = BTreeMap::new();
    for decision in input.compatible_assets {
        if decision.compatible_subject != input.subject
            || compatible_assets
                .insert(decision.asset_digest.clone(), decision)
                .is_some()
        {
            diagnostics.push(closure_diagnostic(
                ConformanceDiagnosticCode::ImplementationClosureIncomplete,
                "asset compatibility decision is duplicated or subject-incompatible",
            ));
        }
    }
    let mut referenced_assets = BTreeSet::new();
    for child in children.values() {
        match &child.subject_binding {
            ClosureSubjectBinding::Subject { identity } if identity == &input.subject => {}
            ClosureSubjectBinding::ReviewedAsset { asset_digest }
                if compatible_assets.contains_key(asset_digest) =>
            {
                referenced_assets.insert(asset_digest.clone());
            }
            _ => diagnostics.push(closure_diagnostic(
                ConformanceDiagnosticCode::ImplementationClosureIncomplete,
                "child closure subject or reviewed asset is incompatible",
            )),
        }
        for (domain, digest) in &child.generated_assets {
            if input.generated_assets.get(domain) != Some(digest) {
                diagnostics.push(closure_diagnostic(
                    ConformanceDiagnosticCode::ImplementationClosureIncomplete,
                    "child generated asset does not match the bundle",
                ));
            }
        }
    }
    if compatible_assets.keys().cloned().collect::<BTreeSet<_>>() != referenced_assets {
        diagnostics.push(closure_diagnostic(
            ConformanceDiagnosticCode::ImplementationClosureIncomplete,
            "asset compatibility set contains an unused or missing decision",
        ));
    }
    if input
        .generated_assets
        .keys()
        .copied()
        .collect::<BTreeSet<_>>()
        != required_generated_assets()
    {
        diagnostics.push(closure_diagnostic(
            ConformanceDiagnosticCode::ImplementationClosureIncomplete,
            "generated asset map is incomplete or unknown",
        ));
    }

    let observed_plan = CanonicalSet::new(input.plan);
    let expected_plan = expected_closure_plan();
    if observed_plan != expected_plan || observed_plan.iter().any(|action| !action.is_consumption())
    {
        diagnostics.push(closure_diagnostic(
            ConformanceDiagnosticCode::ImplementationClosureBoundaryInvalid,
            "closure plan contains replay engine work or incomplete consumption",
        ));
    }
    if let Some(set) = ConformanceDiagnosticSet::new(diagnostics) {
        return Err(set);
    }

    let bundle_digest = canonical_digest(
        DigestDomain::ConformanceClosureBundle,
        &(
            &input.target_digest,
            &input.subject,
            &children,
            &compatible_assets,
            &input.generated_assets,
            &input.platform_matrix_digest,
            &input.rust_security_digest,
            &observed_plan,
        ),
    )
    .map_err(|_| {
        one_closure_diagnostic(
            ConformanceDiagnosticCode::ImplementationClosureIncomplete,
            "closure bundle cannot be encoded canonically",
        )
    })?;
    Ok(ImplementationClosureBundle {
        format_version: input.format_version,
        target_digest: input.target_digest,
        subject: input.subject,
        child_closures: children,
        compatible_assets,
        generated_assets: input.generated_assets,
        platform_matrix_digest: input.platform_matrix_digest,
        rust_security_digest: input.rust_security_digest,
        plan: observed_plan,
        bundle_digest,
    })
}

/// Returns the complete closed child set.
pub fn required_child_closures() -> BTreeSet<ChildClosure> {
    use ChildClosure::*;
    BTreeSet::from([
        Transport,
        ClientLifecycle,
        CoreCodegen,
        EngineIntegration,
        ModuleAuthoring,
        StandaloneClient,
    ])
}

/// Returns every generated asset identity required at closure admission.
pub fn required_generated_assets() -> BTreeSet<GeneratedAssetDomain> {
    use GeneratedAssetDomain::*;
    BTreeSet::from([
        CoreBindings,
        EnginePackage,
        ModuleAssets,
        StandaloneClientAssets,
    ])
}

/// Returns the only admissible consume-only closure plan.
pub fn expected_closure_plan() -> CanonicalSet<ClosurePlanAction> {
    CanonicalSet::new(
        required_child_closures()
            .into_iter()
            .map(ClosurePlanAction::ConsumeChild)
            .chain(
                required_generated_assets()
                    .into_iter()
                    .map(ClosurePlanAction::ConsumeGeneratedAsset),
            )
            .chain([
                ClosurePlanAction::ConsumePlatformMatrix,
                ClosurePlanAction::ConsumeRustSecurity,
            ]),
    )
}

/// Constructs the checked evidence-slot and consume-only plan fixture used before sign-off.
pub fn reviewed_implementation_closure_plan()
-> Result<ImplementationClosurePlanFixture, ConformanceDiagnosticSet> {
    let child_formats = required_child_closures()
        .into_iter()
        .map(|child| (child, format_for_child(child)))
        .collect::<BTreeMap<_, _>>();
    let generated_asset_domains = CanonicalSet::new(required_generated_assets());
    let actions = expected_closure_plan();
    let plan_digest = canonical_digest(
        DigestDomain::ConformanceClosureBundle,
        &(
            &child_formats,
            &generated_asset_domains,
            true,
            true,
            false,
            &actions,
        ),
    )
    .map_err(|_| {
        one_closure_diagnostic(
            ConformanceDiagnosticCode::ImplementationClosureIncomplete,
            "closure plan fixture cannot be encoded canonically",
        )
    })?;
    Ok(ImplementationClosurePlanFixture {
        format_version: ConformanceFormatVersion::V1,
        child_formats,
        generated_asset_domains,
        requires_platform_matrix: true,
        requires_rust_security: true,
        permits_historical_engine_signoff: false,
        actions,
        plan_digest,
    })
}

fn format_for_child(child: ChildClosure) -> ChildEvidenceFormat {
    match child {
        ChildClosure::Transport => ChildEvidenceFormat::TransportObservationRegistry,
        ChildClosure::ClientLifecycle => ChildEvidenceFormat::ClientEvidenceRegistry,
        ChildClosure::CoreCodegen => ChildEvidenceFormat::CoreCodegenEvidenceRegistry,
        ChildClosure::EngineIntegration => {
            ChildEvidenceFormat::EngineIntegrationImplementationClosure
        }
        ChildClosure::ModuleAuthoring => ChildEvidenceFormat::ModuleAuthoringImplementationClosure,
        ChildClosure::StandaloneClient => ChildEvidenceFormat::ClientGenerationClosure,
    }
}

fn child_for_format(format: ChildEvidenceFormat) -> Option<ChildClosure> {
    match format {
        ChildEvidenceFormat::TransportObservationRegistry => Some(ChildClosure::Transport),
        ChildEvidenceFormat::ClientEvidenceRegistry => Some(ChildClosure::ClientLifecycle),
        ChildEvidenceFormat::CoreCodegenEvidenceRegistry => Some(ChildClosure::CoreCodegen),
        ChildEvidenceFormat::EngineIntegrationImplementationClosure => {
            Some(ChildClosure::EngineIntegration)
        }
        ChildEvidenceFormat::EngineIntegrationHistoricalSignoff => None,
        ChildEvidenceFormat::ModuleAuthoringImplementationClosure => {
            Some(ChildClosure::ModuleAuthoring)
        }
        ChildEvidenceFormat::ClientGenerationClosure => Some(ChildClosure::StandaloneClient),
    }
}

fn closure_diagnostic(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
) -> ConformanceDiagnostic {
    ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Closure),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )
}

fn one_closure_diagnostic(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([closure_diagnostic(code, detail)])
        .expect("one closure diagnostic is non-empty")
}
