//! Exact module-authoring scope, ownership correction, and evidence admission.
//!
//! This module is the completeness boundary rather than an implementation shortcut.
//! It retains every reviewed authority row and Rust-policy row as a closed set, routes
//! lifecycle-only harness rows away from authoring, and rejects an observation before
//! producing any status change when target, scope, outcome, or evidence domain differs.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::{
    CanonicalSet, CapabilityId, Digest, EvidenceId, FeatureId, NonEmptyText, Status, TargetDigest,
};

const MODULE_AUTHORING_EXISTING_SCOPE_DIGEST: &str =
    "sha256:2e78e144a19072d7e85483d7496b987904c91f99f2b9f7e567af2f4b6163b7a9";

/// Strict module-authoring scope/evidence wire format.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ModuleAuthoringFormatVersion(u32);

impl ModuleAuthoringFormatVersion {
    /// Returns the only accepted scope/evidence format.
    #[must_use]
    pub const fn current() -> Self {
        Self(1)
    }
}

impl Serialize for ModuleAuthoringFormatVersion {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: serde::Serializer,
    {
        serializer.serialize_u32(self.0)
    }
}

impl<'de> Deserialize<'de> for ModuleAuthoringFormatVersion {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let value = u32::deserialize(deserializer)?;
        if value == 1 {
            Ok(Self(value))
        } else {
            Err(serde::de::Error::custom(
                "unsupported module authoring format version",
            ))
        }
    }
}

/// Authority owning the observable capability represented by a mapping.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleAuthority {
    /// Definitive Go module generator evidence.
    GoCodegen,
    /// Definitive Go module-global client evidence.
    GoClient,
    /// Reviewed Rust-native policy.
    RustPolicy,
}

/// Closed implementation subject responsible for one mapped capability.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleImplementationSubject {
    /// Procedural attributes and typed crate-local bridges.
    AuthoringBridge,
    /// Immutable source snapshot and source compiler.
    SourceCompiler,
    /// Type, metadata, descriptor, registration, and introspection projection.
    TypeProjection,
    /// Parent/argument codecs and production dispatch state machine.
    DispatchRuntime,
    /// Active-session module context and definitive helper mappings.
    ModuleContext,
    /// Generated ownership and scoped regeneration.
    GeneratedAssets,
    /// Scope, local closure, and exact-engine sign-off separation.
    EvidenceBoundary,
}

/// Finite observation domain permitted to prove a module capability.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleEvidenceDomain {
    /// Procedural/source compile fixture.
    CompileFixture,
    /// Pure source/compiler property.
    CompilerProperty,
    /// Engine-independent production dispatcher property.
    DispatchProperty,
    /// Generated ownership and regeneration property.
    AssetProperty,
    /// Warning, documentation, package, and security hygiene.
    SecurityHygiene,
    /// Exact-target engine sign-off.
    ExactEngineSignoff,
    /// Sibling standalone-client work, never admitted here.
    SiblingStandaloneClient,
    /// Feature 5 lifecycle-only engine integration, never authoring evidence.
    LifecycleIntegration,
    /// Cross-platform release evidence, deferred from local closure.
    CrossPlatform,
}

/// Terminal status one complete mapping is allowed to request.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleTerminalStatus {
    /// Direct complete implementation.
    Implemented,
    /// Complete Rust-native behavioural equivalent.
    IdiomaticEquivalent,
    /// Reviewed absence of a sound Rust analogue.
    Inapplicable,
}

/// One exact capability-to-implementation mapping.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleAuthoringMapping {
    /// Stable capability identity.
    pub capability_id: CapabilityId,
    /// Owning authority.
    pub authority: ModuleAuthority,
    /// One approved acceptance-criterion coordinate.
    pub requirement: NonEmptyText,
    /// Concrete Rust implementation subject.
    pub implementation_subject: ModuleImplementationSubject,
    /// Reviewed behavioural rationale.
    pub rationale: NonEmptyText,
    /// Only final status this mapping may request.
    pub allowed_terminal_status: ModuleTerminalStatus,
    /// Smallest observation domain capable of proving the row.
    pub minimum_evidence_domain: ModuleEvidenceDomain,
    /// Exact target to which the mapping applies.
    pub target_digest: TargetDigest,
    /// Whether an unproved row must remain a rendered blocker.
    pub blocker: bool,
}

/// Routing-only correction for one lifecycle capability.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OwnershipCorrection {
    /// Lifecycle capability removed from authoring scope.
    pub capability_id: CapabilityId,
    /// Prior coarse owner.
    pub from: FeatureId,
    /// Correct engine-integration owner.
    pub to: FeatureId,
}

/// Authored scope input retained as lists so duplicate rows remain observable.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleAuthoringScopeInput {
    /// Strict mapping format version.
    pub format_version: ModuleAuthoringFormatVersion,
    /// Reviewed digest of the retained 79 authority rows.
    pub existing_scope_digest: Digest,
    /// Exact target shared by all mappings.
    pub target_digest: TargetDigest,
    /// Retained authority and added Rust-policy rows.
    pub mappings: Vec<ModuleAuthoringMapping>,
    /// Exact 17-row lifecycle ownership correction.
    pub ownership_corrections: Vec<OwnershipCorrection>,
}

/// Duplicate-free exact module-authoring scope safe for evidence admission.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleAuthoringScope {
    target_digest: TargetDigest,
    mapping_digest: Digest,
    mappings: BTreeMap<CapabilityId, ModuleAuthoringMapping>,
    ownership_corrections: BTreeMap<CapabilityId, OwnershipCorrection>,
}

impl ModuleAuthoringScope {
    /// Returns the exact target bound to the complete mapping.
    #[must_use]
    pub const fn target_digest(&self) -> &TargetDigest {
        &self.target_digest
    }

    /// Returns the canonical complete mapping identity.
    #[must_use]
    pub const fn mapping_digest(&self) -> &Digest {
        &self.mapping_digest
    }

    /// Returns every retained and Rust-policy mapping in canonical identity order.
    #[must_use]
    pub const fn mappings(&self) -> &BTreeMap<CapabilityId, ModuleAuthoringMapping> {
        &self.mappings
    }

    /// Returns every corrected lifecycle row in canonical identity order.
    #[must_use]
    pub const fn ownership_corrections(&self) -> &BTreeMap<CapabilityId, OwnershipCorrection> {
        &self.ownership_corrections
    }

    /// Returns all currently unproved blocking identities.
    pub fn blockers(&self) -> CanonicalSet<CapabilityId> {
        CanonicalSet::new(
            self.mappings
                .values()
                .filter(|mapping| mapping.blocker)
                .map(|mapping| mapping.capability_id.clone()),
        )
    }
}

/// Evidence outcome; only `Passed` can request a status transition.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "outcome", rename_all = "kebab-case", deny_unknown_fields)]
pub enum ModuleEvidenceOutcome {
    /// Observation completed successfully.
    Passed { observation_digest: Digest },
    /// Observation ran and failed.
    Failed { diagnostic: NonEmptyText },
    /// Observation did not execute.
    Skipped { reason: NonEmptyText },
}

/// Strict target- and scope-bound module evidence observation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleEvidenceObservation {
    /// Strict observation format version.
    pub format_version: ModuleAuthoringFormatVersion,
    /// Stable evidence identity.
    pub evidence_id: EvidenceId,
    /// Exact checked target.
    pub target_digest: TargetDigest,
    /// Complete mapping identity observed by the producer.
    pub mapping_digest: Digest,
    /// Finite observation domain.
    pub domain: ModuleEvidenceDomain,
    /// Exact capability claims made by this observation.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Pass/fail/skip outcome.
    pub result: ModuleEvidenceOutcome,
}

/// Admission result that makes rejected no-op behaviour explicit.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleAuthoringEvidenceAdmission {
    /// Permitted status changes; empty for every rejected observation.
    pub status_changes: BTreeMap<CapabilityId, Status>,
    /// Every unclosed blocker after considering this one observation.
    pub blockers: CanonicalSet<CapabilityId>,
    /// Stable rejection reason, absent only for admitted evidence.
    pub rejection: Option<NonEmptyText>,
}

/// Constructs the one reviewed 79/32 scope plus 17-row ownership correction.
pub fn module_authoring_scope_input(target_digest: TargetDigest) -> ModuleAuthoringScopeInput {
    let mappings = FEATURE6_EXISTING_IDS
        .iter()
        .chain(FEATURE6_POLICY_IDS.iter())
        .map(|id| mapping(id, &target_digest))
        .collect();
    let ownership_corrections = FEATURE6_LIFECYCLE_CORRECTIONS
        .iter()
        .map(|id| OwnershipCorrection {
            capability_id: capability(id),
            from: FeatureId::Feature6,
            to: FeatureId::Feature5,
        })
        .collect();
    ModuleAuthoringScopeInput {
        format_version: ModuleAuthoringFormatVersion::current(),
        existing_scope_digest: Digest::new(MODULE_AUTHORING_EXISTING_SCOPE_DIGEST)
            .expect("reviewed module-authoring scope digest is valid"),
        target_digest,
        mappings,
        ownership_corrections,
    }
}

/// Validates exact set membership, target, per-row policy, and routing correction.
pub fn derive_module_authoring_scope(
    input: &ModuleAuthoringScopeInput,
    expected_target: &TargetDigest,
) -> Result<ModuleAuthoringScope, NonEmptyText> {
    let expected_digest = Digest::new(MODULE_AUTHORING_EXISTING_SCOPE_DIGEST)
        .expect("reviewed module-authoring scope digest is valid");
    if input.existing_scope_digest != expected_digest || &input.target_digest != expected_target {
        return Err(reason(
            "module authoring scope or target differs from review",
        ));
    }

    let expected_ids = expected_mapping_ids();
    let mut mappings = BTreeMap::new();
    for mapping in &input.mappings {
        if mappings
            .insert(mapping.capability_id.clone(), mapping.clone())
            .is_some()
        {
            return Err(reason("module authoring mapping contains a duplicate row"));
        }
        if mapping != &self::mapping(mapping.capability_id.as_str(), &input.target_digest) {
            return Err(reason(
                "module authoring mapping contains a stale or incomplete row",
            ));
        }
    }
    if CanonicalSet::new(mappings.keys().cloned()) != expected_ids {
        return Err(reason(
            "module authoring mapping is not the exact 79/32 set",
        ));
    }

    let expected_corrections = CanonicalSet::new(
        FEATURE6_LIFECYCLE_CORRECTIONS
            .iter()
            .map(|id| capability(id)),
    );
    let mut ownership_corrections = BTreeMap::new();
    for correction in &input.ownership_corrections {
        if correction.from != FeatureId::Feature6 || correction.to != FeatureId::Feature5 {
            return Err(reason(
                "module lifecycle correction has the wrong owner transition",
            ));
        }
        if ownership_corrections
            .insert(correction.capability_id.clone(), correction.clone())
            .is_some()
        {
            return Err(reason(
                "module lifecycle correction contains a duplicate row",
            ));
        }
    }
    if CanonicalSet::new(ownership_corrections.keys().cloned()) != expected_corrections
        || ownership_corrections
            .keys()
            .any(|id| mappings.contains_key(id))
    {
        return Err(reason(
            "module lifecycle correction is not the exact disjoint 17-row set",
        ));
    }

    let mapping_digest = canonical_digest(DigestDomain::ModuleAuthoring, &mappings)
        .map_err(|_| reason("module authoring mapping could not be hashed"))?;
    Ok(ModuleAuthoringScope {
        target_digest: input.target_digest.clone(),
        mapping_digest,
        mappings,
        ownership_corrections,
    })
}

/// Admits one capability-local observation or returns an explicit no-op rejection.
pub fn admit_module_authoring_evidence(
    scope: &ModuleAuthoringScope,
    observation: &ModuleEvidenceObservation,
) -> ModuleAuthoringEvidenceAdmission {
    let reject = |message: &'static str| ModuleAuthoringEvidenceAdmission {
        status_changes: BTreeMap::new(),
        blockers: scope.blockers(),
        rejection: Some(reason(message)),
    };
    if observation.target_digest != scope.target_digest
        || observation.mapping_digest != scope.mapping_digest
    {
        return reject("module evidence is stale or target-incompatible");
    }
    if !matches!(observation.result, ModuleEvidenceOutcome::Passed { .. }) {
        return reject("failed or skipped module evidence cannot change status");
    }
    if observation.capability_ids.is_empty() {
        return reject("module evidence proves no capability");
    }

    let mut status_changes = BTreeMap::new();
    for capability_id in observation.capability_ids.iter() {
        let Some(mapping) = scope.mappings.get(capability_id) else {
            return reject("module evidence claims a sibling or unknown capability");
        };
        if mapping.minimum_evidence_domain != observation.domain {
            return reject("module evidence domain cannot prove the claimed capability");
        }
        status_changes.insert(
            capability_id.clone(),
            match mapping.allowed_terminal_status {
                ModuleTerminalStatus::Implemented => Status::Implemented,
                ModuleTerminalStatus::IdiomaticEquivalent => Status::IdiomaticEquivalent,
                ModuleTerminalStatus::Inapplicable => Status::Inapplicable,
            },
        );
    }

    ModuleAuthoringEvidenceAdmission {
        blockers: CanonicalSet::new(
            scope
                .blockers()
                .iter()
                .filter(|id| !status_changes.contains_key(*id))
                .cloned(),
        ),
        status_changes,
        rejection: None,
    }
}

fn mapping(id: &str, target_digest: &TargetDigest) -> ModuleAuthoringMapping {
    let authority = authority_for(id);
    let implementation_subject = subject_for(id, authority);
    let minimum_evidence_domain = evidence_for(id, implementation_subject);
    ModuleAuthoringMapping {
        capability_id: capability(id),
        authority,
        requirement: NonEmptyText::new(requirement_for(id, authority))
            .expect("reviewed requirement coordinate is valid"),
        implementation_subject,
        rationale: NonEmptyText::new(rationale_for(authority))
            .expect("reviewed mapping rationale is valid"),
        allowed_terminal_status: terminal_for(id, authority),
        minimum_evidence_domain,
        target_digest: target_digest.clone(),
        blocker: true,
    }
}

fn authority_for(id: &str) -> ModuleAuthority {
    if id.starts_with("behavior/go-codegen/") {
        ModuleAuthority::GoCodegen
    } else if id.starts_with("behavior/go-client/") {
        ModuleAuthority::GoClient
    } else {
        ModuleAuthority::RustPolicy
    }
}

fn subject_for(id: &str, authority: ModuleAuthority) -> ModuleImplementationSubject {
    if authority == ModuleAuthority::GoClient
        || id.contains("active-session-context")
        || id.contains("self-dependency-context")
    {
        ModuleImplementationSubject::ModuleContext
    } else if id.contains("generated-asset") || id.contains("regeneration") {
        ModuleImplementationSubject::GeneratedAssets
    } else if id.contains("dispatch")
        || id.contains("argument")
        || id.contains("result")
        || id.contains("panic")
        || id.contains("cancellation")
        || id.contains("call-isolation")
        || id.contains("parent-state")
        || id.contains("object-id")
    {
        ModuleImplementationSubject::DispatchRuntime
    } else if id.contains("explicit-export") || id.contains("authoring-single-source") {
        ModuleImplementationSubject::AuthoringBridge
    } else if id.contains("engine-free")
        || id.contains("signoff-boundary")
        || id.contains("source-coordinate-diagnostics")
    {
        ModuleImplementationSubject::EvidenceBoundary
    } else if authority == ModuleAuthority::GoCodegen
        || id.contains("descriptor")
        || id.contains("typedef")
        || id.contains("type-mapping")
        || id.contains("optional-default")
        || id.contains("function-")
        || id.contains("interface")
        || id.contains("enum")
        || id.contains("scalar")
        || id.contains("object-state")
        || id.contains("private-state")
        || id.contains("wire-name")
        || id.contains("root-constructor")
    {
        ModuleImplementationSubject::TypeProjection
    } else {
        ModuleImplementationSubject::SourceCompiler
    }
}

fn evidence_for(id: &str, subject: ModuleImplementationSubject) -> ModuleEvidenceDomain {
    if id.contains("exact-engine-signoff-boundary") {
        ModuleEvidenceDomain::ExactEngineSignoff
    } else {
        match subject {
            ModuleImplementationSubject::AuthoringBridge => ModuleEvidenceDomain::CompileFixture,
            ModuleImplementationSubject::SourceCompiler
            | ModuleImplementationSubject::TypeProjection => ModuleEvidenceDomain::CompilerProperty,
            ModuleImplementationSubject::DispatchRuntime
            | ModuleImplementationSubject::ModuleContext => ModuleEvidenceDomain::DispatchProperty,
            ModuleImplementationSubject::GeneratedAssets => ModuleEvidenceDomain::AssetProperty,
            ModuleImplementationSubject::EvidenceBoundary => ModuleEvidenceDomain::SecurityHygiene,
        }
    }
}

fn terminal_for(id: &str, authority: ModuleAuthority) -> ModuleTerminalStatus {
    if authority == ModuleAuthority::GoClient
        && (id.contains("%254%41%2553%254%46%254%45") || id.contains("%254%43%254%43%254%44"))
    {
        // These generated Go globals are type aliases with no sound Rust symbol; their
        // observable operations remain available through the typed module context.
        ModuleTerminalStatus::IdiomaticEquivalent
    } else {
        ModuleTerminalStatus::Implemented
    }
}

fn requirement_for(id: &str, authority: ModuleAuthority) -> &'static str {
    if authority == ModuleAuthority::GoClient {
        "12.9"
    } else if id.contains("explicit-export") {
        "2.1"
    } else if id.contains("authoring-single-source") {
        "2.6"
    } else if id.contains("source-discovery") {
        "3.3"
    } else if id.contains("source-coordinate") {
        "14.2"
    } else if id.contains("wire-name") {
        "7.14"
    } else if id.contains("root-constructor") {
        "4.10"
    } else if id.contains("object-state") {
        "4.2"
    } else if id.contains("private-state") {
        "4.4"
    } else if id.contains("interface") {
        "5.1"
    } else if id.contains("enum") {
        "5.6"
    } else if id.contains("custom-scalar") {
        "5.11"
    } else if id.contains("type-mapping") {
        "6.1"
    } else if id.contains("optional-default") {
        "6.7"
    } else if id.contains("function-shape") {
        "7.1"
    } else if id.contains("function-metadata") {
        "7.6"
    } else if id.contains("canonical-descriptor") {
        "8.1"
    } else if id.contains("typedef-introspection") {
        "8.7"
    } else if id.contains("dispatch-totality") {
        "9.4"
    } else if id.contains("parent-state") {
        "10.1"
    } else if id.contains("named-argument") {
        "10.4"
    } else if id.contains("object-id") {
        "10.12"
    } else if id.contains("result-single") {
        "11.10"
    } else if id.contains("application-error") {
        "11.6"
    } else if id.contains("panic") {
        "11.8"
    } else if id.contains("active-session") {
        "12.1"
    } else if id.contains("self-dependency") {
        "12.5"
    } else if id.contains("call-isolation") {
        "13.1"
    } else if id.contains("cancellation") {
        "13.7"
    } else if id.contains("generated-asset") {
        "15.1"
    } else if id.contains("regeneration") {
        "15.7"
    } else if id.contains("engine-free") {
        "16.1"
    } else if id.contains("signoff-boundary") {
        "17.9"
    } else {
        "8.2"
    }
}

const fn rationale_for(authority: ModuleAuthority) -> &'static str {
    match authority {
        ModuleAuthority::GoCodegen => {
            "Preserve the definitive observable generator behaviour through an idiomatic typed Rust compiler"
        }
        ModuleAuthority::GoClient => {
            "Preserve the definitive helper capability through call-scoped Rust context without process-global state"
        }
        ModuleAuthority::RustPolicy => {
            "Make the approved Rust-native authoring and dispatch obligation independently provable"
        }
    }
}

fn expected_mapping_ids() -> CanonicalSet<CapabilityId> {
    CanonicalSet::new(
        FEATURE6_EXISTING_IDS
            .iter()
            .chain(FEATURE6_POLICY_IDS.iter())
            .map(|id| capability(id)),
    )
}

fn capability(id: &str) -> CapabilityId {
    CapabilityId::new(id).expect("reviewed module capability identity is valid")
}

fn reason(message: &'static str) -> NonEmptyText {
    NonEmptyText::new(message).expect("static module scope diagnostic is valid")
}

const FEATURE6_POLICY_IDS: &[&str] = &[
    "policy/rust-policy/module-explicit-export",
    "policy/rust-policy/module-authoring-single-source",
    "policy/rust-policy/module-source-discovery-closure",
    "policy/rust-policy/module-source-coordinate-diagnostics",
    "policy/rust-policy/module-wire-name-collision",
    "policy/rust-policy/module-root-constructor",
    "policy/rust-policy/module-object-state",
    "policy/rust-policy/module-private-state",
    "policy/rust-policy/module-interface-contract",
    "policy/rust-policy/module-enum-contract",
    "policy/rust-policy/module-custom-scalar-contract",
    "policy/rust-policy/module-type-mapping-closure",
    "policy/rust-policy/module-optional-default-semantics",
    "policy/rust-policy/module-function-shape-closure",
    "policy/rust-policy/module-function-metadata",
    "policy/rust-policy/module-canonical-descriptor",
    "policy/rust-policy/module-typedef-introspection-equivalence",
    "policy/rust-policy/module-dispatch-totality",
    "policy/rust-policy/module-parent-state-decoding",
    "policy/rust-policy/module-named-argument-decoding",
    "policy/rust-policy/module-object-id-reentry",
    "policy/rust-policy/module-result-single-assignment",
    "policy/rust-policy/module-application-error-reporting",
    "policy/rust-policy/module-panic-containment",
    "policy/rust-policy/module-active-session-context",
    "policy/rust-policy/module-self-dependency-context",
    "policy/rust-policy/module-call-isolation",
    "policy/rust-policy/module-cancellation",
    "policy/rust-policy/module-generated-asset-ownership",
    "policy/rust-policy/module-change-triggered-regeneration",
    "policy/rust-policy/module-engine-free-local-checkpoint",
    "policy/rust-policy/module-exact-engine-signoff-boundary",
];

const FEATURE6_LIFECYCLE_CORRECTIONS: &[&str] = &[
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fdeps-list-succeeds",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fengine-required-reports-version",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fgenerate-exposes-generator",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fgenerate-respects-cwd",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fgenerate-succeeds",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-does-not-remove-existing-files",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-does-not-write-config",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-honors-custom-path",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-module-seeds-files",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-records-authoring-sdk",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-registers-module",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-scaffolds-module",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finit-writes-module-config",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finstall-marks-as-sdk",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Finstall-registers-sdk",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fscaffolded-module-loads",
    "behavior/sdk-contract-harness/source%2Fsdk-contract-harness%2Fharness-check%2Fsdk-reports-module-options",
];

const FEATURE6_EXISTING_IDS: &[&str] = &[
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%41%2553%254%46%254%45",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%43%254%43%254%44",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%44odule",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%44odule%2553ource",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%254%45ode",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2541ddress",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543ache%2556olume",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543hangeset",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543lose",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543loud",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543ontainer",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%254%44odule",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%254%45ode",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%2546unction%2543all",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%2554ype%2544efs",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2543urrent%2557orkspace",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2544efault%2550latform",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2544irectory",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545ngine",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545ngine%2556olume",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545nv%2546ile",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2545rror",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2546ile",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2546unction",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2547enerated%2543ode",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2547it",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2548%2554%2554%2550",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2548ost",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2549%2544",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553chema",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553ecret",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553et%2553ecret",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553ource%254%44ap",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2553shfs%2556olume",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2554ype%2544ef",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdag%2F%2556ersion",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-dynamic-subtest%2Ftemplates%2F%2554est%2550arse%2550ragma%2543omment%2F%253%43dynamic%253%41de17bc76acebbaa7%253%410%253%45",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-function%2Ftemplates%2F%254%45ew%254%44odule%2549ntrospection%2545mitter",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-function%2Ftemplates%2F%2547o%2554emplate%2546uncs",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-subtest%2Ftemplates%2F%2554est%2556isit%2554ypes%2544eterministic%2541cross%2546iles%2Fgitrepo%2520method%2520order",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-subtest%2Ftemplates%2F%2554est%2556isit%2554ypes%2544eterministic%2541cross%2546iles%2Fstruct%2520order",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%43egacy%254%44odule%2549nterface%2549%2544%2553urface",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%44odule%2549ntrospection%254%41%2553%254%46%254%45_%2550arseable",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%44odule%2549ntrospection%254%41%2553%254%46%254%45_%2552ound%2554rips%2554hrough%254%44erge",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%45amespace%2554ype%254%45ame",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%254%46bject%2555nmarshal%2549mported%2549nterface%2546ield",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2542asic",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2553kip%2543ontext%2541rg",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2553kips%2552eserved%2549%2544",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2556oid%2552eturn",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%254%46bject_%2557ith%2546ield",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%254%46bjects%2550ass%2542y%2549%2544",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%254%46ptional%254%45on%254%45ull%2553tripping",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%2545num%2544efault",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%2550ointer%254%46bject%2545num%2552equired",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2541rg_%2556ariadic%2541nd%2544efault%2553tay%2552equired",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2545num_%2542asic",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2545num_%2543onventional%254%44ember%254%45ames",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2549nterface_%2542asic",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%254%45ame_%254%43ocal%2556s%2546oreign",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%254%46bject%2552ef",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%254%46bject%2552ef%2550ointer",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2545num%2552ef",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2549face%2552ef",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2542ool",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2546loat",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2549nt",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2550rimitive%2550ointer",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2553lice",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2553lice%254%46f%254%45on%254%45ull%254%46bjects",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2549ntrospect%2554ype%2552ef_%2553lice%254%46f%254%46bjects",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2550arse%2547o%2549face%2541ccepts%2549mported%2544agger%254%46bject",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2550arse%2550ragma%2543omment",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Ftemplates%2F%2554est%2556isit%2554ypes%2544eterministic%2541cross%2546iles",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test-table%2Ftemplates%2F%2554est%2550arse%2550ragma%2543omment%2F%253%43table%253%41de17bc76acebbaa7%253%410%253%45",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Ftemplates%2F%254%44odule%2549ntrospection%2545mitter",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Ftemplates%2F%254%45amed%2550arsed%2554ype",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Ftemplates%2F%2550arsed%2554ype",
];

#[cfg(test)]
mod tests {
    use super::{
        FEATURE6_EXISTING_IDS, FEATURE6_LIFECYCLE_CORRECTIONS, FEATURE6_POLICY_IDS,
        derive_module_authoring_scope, module_authoring_scope_input,
    };
    use crate::model::{Digest, TargetDigest};

    #[test]
    fn reviewed_partition_has_exact_counts_and_is_disjoint() {
        assert_eq!(FEATURE6_EXISTING_IDS.len(), 79);
        assert_eq!(FEATURE6_POLICY_IDS.len(), 32);
        assert_eq!(FEATURE6_LIFECYCLE_CORRECTIONS.len(), 17);
        let existing = FEATURE6_EXISTING_IDS
            .iter()
            .copied()
            .collect::<std::collections::BTreeSet<_>>();
        let policies = FEATURE6_POLICY_IDS
            .iter()
            .copied()
            .collect::<std::collections::BTreeSet<_>>();
        let corrections = FEATURE6_LIFECYCLE_CORRECTIONS
            .iter()
            .copied()
            .collect::<std::collections::BTreeSet<_>>();
        assert!(existing.is_disjoint(&policies));
        assert!(existing.is_disjoint(&corrections));
        assert!(policies.is_disjoint(&corrections));
    }

    #[test]
    fn reviewed_scope_derives_without_mutating_status() {
        let target = TargetDigest::new(Digest::sha256(b"module-authoring-target"));
        let input = module_authoring_scope_input(target.clone());
        let scope = derive_module_authoring_scope(&input, &target).expect("reviewed scope derives");
        assert_eq!(scope.mappings().len(), 111);
        assert_eq!(scope.ownership_corrections().len(), 17);
        assert_eq!(scope.blockers().len(), 111);
    }
}
