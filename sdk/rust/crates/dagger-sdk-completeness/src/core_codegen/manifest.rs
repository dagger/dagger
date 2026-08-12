//! Exhaustive binding closure between reviewed capabilities and generated Rust semantics.
//!
//! Schema rows join through exact canonical coordinates and binding kinds. Compatibility
//! rows join only through authored records carrying the complete authority kind and source
//! fingerprint. This keeps the large generated surface reviewable without introducing a
//! name-based fallback that could silently adopt a new or changed declaration.

use std::collections::{BTreeMap, BTreeSet};

use dagger_codegen::projection::catalog::{
    BindingDescriptor, BindingKey, BindingKind as CatalogBindingKind, CatalogDisposition,
    ProjectionCatalog,
};
use dagger_codegen::schema::canonical::{SchemaCoordinate, SchemaName};
use dagger_codegen::target::CodegenTarget;
use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_bytes, canonical_digest, decode_canonical};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    AuthorityId, CanonicalSet, CapabilityId, CapabilityKind, CapabilityRecord, CommitSha,
    DecisionId, Digest, FeatureId, PolicyId, RepositoryRelativePath, ResolvedLedger, Status,
};

const MANIFEST_FORMAT_VERSION: u32 = 1;
const MAPPINGS_FORMAT_VERSION: u32 = 1;
const APPROVED_RETAINED_SCOPE_DIGEST: &str =
    "sha256:2b46180b54356faf2071a91198afd1a0e40a757b57a1686f579d2f9ab6ed583f";
const GENERATED_CLIENT_IMPLEMENTATION_EVIDENCE: &str =
    "implementation/core-codegen/generated-client";
const GENERATED_CLIENT_VERIFICATION_EVIDENCE: &str = "verification/core-codegen/release-closure";

/// Executable proof domain required by one generated binding.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum EvidenceDomain {
    /// Final source or handwritten runtime bytes implement the declared semantic fingerprint.
    Implementation,
    /// A generated-domain property covers the binding through its projection catalog.
    Property,
    /// The public symbol or intended negative contract is compiler-checked.
    Compile,
    /// The exact field and argument Wire_Names are observed in a constructed document.
    QueryProjection,
    /// Warning-denied rustdoc covers the public item or documented policy.
    Documentation,
    /// A representative operation in this runtime category succeeds against the exact engine.
    ExactTarget,
    /// A reviewed decision explains a deliberately different idiomatic Rust shape.
    Decision,
}

/// How an authored compatibility mapping is represented by the Rust implementation.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum MappingDisposition {
    /// The declaration is represented by a closed Rust mapping policy.
    MappingPolicy,
    /// The public shape differs materially and is justified by a reviewed decision.
    IdiomaticEquivalent,
}

/// One explicit compatibility rule whose complete capability expansion is authored.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewedMappingRule {
    /// Identity duplicated from the map key so key substitution is detectable.
    pub policy_id: PolicyId,
    /// Authority whose declarations are being mapped.
    pub authority_id: AuthorityId,
    /// Complete source declaration category shared by this closed set.
    pub capability_kind: CapabilityKind,
    /// Exact number of declarations in the rule expansion.
    pub expected_count: usize,
    /// Explicit, sorted capability identities; no selector or fallback expands this set.
    pub capability_ids: CanonicalSet<CapabilityId>,
    /// Digest of ordered capability identity and semantic-fingerprint pairs.
    pub capability_fingerprints_digest: Digest,
    /// Whether the result is a direct policy or an idiomatic public-shape decision.
    pub disposition: MappingDisposition,
    /// Reviewed decision required for a materially different public shape.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub decision_id: Option<DecisionId>,
    /// Non-empty executable domains required before the capability can close.
    pub required_evidence: BTreeSet<EvidenceDomain>,
}

/// Closed authored mappings for every non-schema capability retained by core generation.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CoreCodegenMappings {
    /// Mapping document schema version.
    pub format_version: u32,
    /// Exact Dagger revision whose declarations were reviewed.
    pub target_revision: CommitSha,
    /// Reviewed digest of the retained pre-policy capability scope.
    pub retained_scope_digest: Digest,
    /// Complete set of decisions used by idiomatic-equivalent mappings.
    pub decisions: BTreeSet<DecisionId>,
    /// Exact non-schema mapping rules keyed by policy identity.
    pub rules: BTreeMap<PolicyId, ReviewedMappingRule>,
}

impl CoreCodegenMappings {
    /// Decodes only the canonical JSON representation used for review and hashing.
    pub fn decode(bytes: &[u8]) -> Result<Self, crate::canonical::CanonicalError> {
        decode_canonical(bytes)
    }
}

/// Generated source category recorded without depending on the publisher crate.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum GeneratedArtifactKind {
    /// One generated Rust module.
    RustModule,
    /// One generated compile or projection test.
    RustTest,
}

/// Exact-target ownership header recovered from a formatted generated artifact.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratedArtifactProvenance {
    /// Generator format identity.
    pub format: String,
    /// Component with permission to replace this path.
    pub ownership: String,
    /// Schema digest from which the artifact was projected.
    pub schema_digest: String,
    /// Dagger revision from which the artifact was projected.
    pub target_revision: String,
}

/// Final byte and semantic identities of one formatted generated artifact.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratedArtifactRecord {
    /// Artifact category used by publication policy.
    pub kind: GeneratedArtifactKind,
    /// Digest of final formatted bytes.
    pub sha256: Digest,
    /// Digest of syntax tokens independent of formatting.
    pub semantic_sha256: Digest,
    /// Machine-readable source ownership header.
    pub provenance: GeneratedArtifactProvenance,
}

/// Durable representation chosen for one completeness capability.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ManifestBindingKind {
    /// A generated or handwritten public Rust symbol.
    PublicSymbol,
    /// A private handwritten execution or scalar strategy.
    RuntimeStrategy,
    /// A closed compatibility or target-inactive policy.
    MappingPolicy,
    /// A materially different public shape with reviewed rationale.
    IdiomaticEquivalent,
    /// A validated directive definition with no active target applications.
    TargetInactiveDirective,
}

/// One exhaustive capability-to-implementation join record.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BindingRecord {
    /// Capability represented by this record.
    pub capability_id: CapabilityId,
    /// Authority defining the capability.
    pub authority_id: AuthorityId,
    /// Fingerprint of the authoritative capability declaration.
    pub capability_fingerprint: Digest,
    /// Rust representation category.
    pub binding_kind: ManifestBindingKind,
    /// Exact GraphQL coordinate for schema-owned records.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wire_coordinate: Option<String>,
    /// Exact public Rust symbol when the representation owns one.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub rust_symbol: Option<String>,
    /// Exact compatibility policy when no one-to-one public symbol exists.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub policy_id: Option<PolicyId>,
    /// Reviewed decision for a materially different Rust public shape.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub decision_id: Option<DecisionId>,
    /// Semantic identity of the current projection or policy implementation.
    pub implementation_fingerprint: Digest,
    /// Complete evidence domains required for status closure.
    pub required_evidence: BTreeSet<EvidenceDomain>,
}

/// Canonical generated binding manifest consumed by publication and evidence validation.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratedBindingManifest {
    /// Manifest schema version.
    pub format_version: u32,
    /// Exact Dagger revision represented by every record.
    pub target_revision: String,
    /// Exact checked schema digest.
    pub schema_digest: String,
    /// Reviewed retained-scope identity.
    pub retained_scope_digest: Digest,
    /// Semantic digest of the complete projection catalog.
    pub projection_fingerprint: Digest,
    /// Complete formatted artifact set, excluding this manifest itself.
    pub artifacts: BTreeMap<RepositoryRelativePath, GeneratedArtifactRecord>,
    /// Exactly one record for every active core-codegen capability.
    pub bindings: BTreeMap<CapabilityId, BindingRecord>,
}

impl GeneratedBindingManifest {
    /// Encodes deterministic canonical JSON with one terminal newline.
    pub fn encode(&self) -> Result<Vec<u8>, crate::canonical::CanonicalError> {
        canonical_bytes(self)
    }

    /// Decodes only the canonical manifest representation.
    pub fn decode(bytes: &[u8]) -> Result<Self, crate::canonical::CanonicalError> {
        decode_canonical(bytes)
    }
}

/// Assembles the exact capability join without deriving or mutating completeness status.
pub fn assemble_core_codegen_manifest(
    target: &CodegenTarget,
    ledger: &ResolvedLedger,
    mappings: &CoreCodegenMappings,
    catalog: &ProjectionCatalog,
    artifacts: BTreeMap<RepositoryRelativePath, GeneratedArtifactRecord>,
) -> Validation<GeneratedBindingManifest> {
    let mut diagnostics = DiagnosticCollector::default();
    validate_mapping_header(target, mappings, &mut diagnostics);

    // JSON object keys cannot carry the catalog's structured semantic identity. Encode
    // the already ordered map as key/value tuples so the fingerprint covers both
    // halves without weakening the key to a display string.
    let ordered_catalog = catalog.bindings().iter().collect::<Vec<_>>();
    let projection_fingerprint = match canonical_digest(DigestDomain::Artifact, &ordered_catalog) {
        Ok(digest) => digest,
        Err(_) => {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityFingerprintMismatch,
                "projection-catalog",
                "projection catalog could not be fingerprinted",
            ));
            Digest::sha256([])
        }
    };

    let active = ledger
        .capabilities
        .iter()
        .filter(|(_, row)| is_core_codegen_capability(row))
        .collect::<BTreeMap<_, _>>();
    let retained_ids = active
        .iter()
        .filter(|(_, row)| row.authority_id.as_str() != "rust-policy")
        .map(|(capability_id, _)| (*capability_id).clone())
        .collect::<Vec<_>>();
    let retained_digest = serde_json::to_vec(&retained_ids)
        .map(Digest::sha256)
        .unwrap_or_else(|_| Digest::sha256([]));
    if mappings.retained_scope_digest.as_str() != APPROVED_RETAINED_SCOPE_DIGEST
        || mappings.retained_scope_digest != retained_digest
    {
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::CapabilityFingerprintMismatch,
            "retained-scope",
            "retained capability IDs differ from the reviewed exact-scope digest",
        ));
    }
    let expected_mappings = active
        .iter()
        .filter(|(_, row)| row.authority_id.as_str() != "engine-schema")
        .map(|(capability_id, _)| (*capability_id).clone())
        .collect::<BTreeSet<_>>();
    let expanded_mappings = expand_mappings(&active, mappings, &mut diagnostics);
    let actual_mappings = expanded_mappings.keys().cloned().collect::<BTreeSet<_>>();
    report_set_difference(
        &expected_mappings,
        &actual_mappings,
        "reviewed compatibility mapping",
        &mut diagnostics,
    );

    let mut bindings = BTreeMap::new();
    for (capability_id, row) in active {
        let binding = if row.authority_id.as_str() == "engine-schema" {
            schema_binding(row, catalog, &mut diagnostics)
        } else {
            expanded_mappings.get(capability_id).and_then(|mapping| {
                mapped_binding(
                    capability_id,
                    row,
                    mapping,
                    mappings,
                    &projection_fingerprint,
                    &mut diagnostics,
                )
            })
        };
        if let Some(binding) = binding
            && bindings.insert(capability_id.clone(), binding).is_some()
        {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityBindingDuplicate,
                capability_id,
                "capability received more than one binding record",
            ));
        }
    }

    let used_decisions = mappings
        .rules
        .values()
        .filter_map(|rule| rule.decision_id.clone())
        .collect::<BTreeSet<_>>();
    if used_decisions != mappings.decisions {
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::DecisionEvidenceInvalid,
            "mapping-decisions",
            "reviewed decisions must equal the exact set used by idiomatic mappings",
        ));
    }

    let manifest = GeneratedBindingManifest {
        format_version: MANIFEST_FORMAT_VERSION,
        target_revision: target.dagger_revision().as_str().to_owned(),
        schema_digest: target.schema_digest().to_string(),
        retained_scope_digest: mappings.retained_scope_digest.clone(),
        projection_fingerprint,
        artifacts,
        bindings,
    };
    validate_binding_bijection(ledger, &manifest, &mut diagnostics);
    diagnostics.finish(manifest)
}

/// Validates only the exact key/fingerprint/evidence bijection of an assembled manifest.
///
/// This boundary is intentionally independent from target projection so property tests and
/// downstream readers can reject missing, extra, duplicate-by-identity, or malformed records
/// without silently rebuilding a replacement manifest.
pub fn validate_core_codegen_bijection(
    ledger: &ResolvedLedger,
    manifest: &GeneratedBindingManifest,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    validate_binding_bijection(ledger, manifest, &mut diagnostics);
    diagnostics.finish(())
}

/// Recomputes manifest assembly and requires byte-semantic equality with a candidate.
pub fn validate_core_codegen_manifest(
    candidate: &GeneratedBindingManifest,
    target: &CodegenTarget,
    ledger: &ResolvedLedger,
    mappings: &CoreCodegenMappings,
    catalog: &ProjectionCatalog,
    artifacts: BTreeMap<RepositoryRelativePath, GeneratedArtifactRecord>,
) -> Validation<()> {
    let expected = assemble_core_codegen_manifest(target, ledger, mappings, catalog, artifacts)?;
    if candidate == &expected {
        Ok(())
    } else {
        let mut diagnostics = DiagnosticCollector::default();
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::CapabilityFingerprintMismatch,
            "binding-manifest",
            "binding manifest differs from the exact current join",
        ));
        diagnostics.finish(())
    }
}

fn validate_mapping_header(
    target: &CodegenTarget,
    mappings: &CoreCodegenMappings,
    diagnostics: &mut DiagnosticCollector,
) {
    if mappings.format_version != MAPPINGS_FORMAT_VERSION {
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::FormatUnsupported,
            "core-codegen-mappings",
            "mapping format version is unsupported",
        ));
    }
    if mappings.target_revision.as_str() != target.dagger_revision().as_str() {
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::TargetRevisionInvalid,
            "core-codegen-mappings",
            "mapping target revision differs from the exact target",
        ));
    }
}

fn schema_binding(
    row: &CapabilityRecord,
    catalog: &ProjectionCatalog,
    diagnostics: &mut DiagnosticCollector,
) -> Option<BindingRecord> {
    let (coordinate, allowed_kinds) = match schema_coordinate(row) {
        Ok(value) => value,
        Err(detail) => {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityMappingInvalid,
                &row.capability_id,
                detail,
            ));
            return None;
        }
    };
    let candidates = catalog
        .bindings()
        .iter()
        .filter(|(key, _)| {
            key.wire_coordinate.as_ref() == Some(&coordinate)
                && allowed_kinds.contains(&key.binding_kind)
        })
        .collect::<Vec<_>>();
    if candidates.len() != 1 {
        diagnostics.push(binding_diagnostic(
            if candidates.is_empty() {
                DiagnosticCode::CapabilityBindingMissing
            } else {
                DiagnosticCode::CapabilityBindingDuplicate
            },
            &row.capability_id,
            "schema capability must resolve to exactly one coordinate-and-kind binding",
        ));
        return None;
    }
    let (key, descriptor) = candidates[0];
    Some(binding_from_catalog(row, key, descriptor))
}

fn mapped_binding(
    capability_id: &CapabilityId,
    row: &CapabilityRecord,
    mapping: &ReviewedMappingRule,
    mappings: &CoreCodegenMappings,
    projection_fingerprint: &Digest,
    diagnostics: &mut DiagnosticCollector,
) -> Option<BindingRecord> {
    if mapping.required_evidence.is_empty() {
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::CapabilityEvidenceIncomplete,
            capability_id,
            "mapping declares no executable evidence domains",
        ));
        return None;
    }
    match mapping.disposition {
        MappingDisposition::MappingPolicy if mapping.decision_id.is_some() => {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::DecisionEvidenceInvalid,
                capability_id,
                "a direct mapping policy cannot carry an idiomatic-equivalence decision",
            ));
            return None;
        }
        MappingDisposition::IdiomaticEquivalent
            if !mapping
                .decision_id
                .as_ref()
                .is_some_and(|decision| mappings.decisions.contains(decision)) =>
        {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::DecisionEvidenceInvalid,
                capability_id,
                "idiomatic mapping lacks a decision from the reviewed closed set",
            ));
            return None;
        }
        MappingDisposition::MappingPolicy | MappingDisposition::IdiomaticEquivalent => {}
    }

    let implementation_fingerprint = match canonical_digest(
        DigestDomain::RuleExpansion,
        &(
            capability_id,
            &row.capability_fingerprint,
            &mapping.policy_id,
            mapping.disposition,
            &mapping.decision_id,
            &mapping.required_evidence,
            projection_fingerprint,
        ),
    ) {
        Ok(digest) => digest,
        Err(_) => {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityFingerprintMismatch,
                capability_id,
                "mapping policy could not be fingerprinted",
            ));
            return None;
        }
    };
    Some(BindingRecord {
        capability_id: capability_id.clone(),
        authority_id: row.authority_id.clone(),
        capability_fingerprint: row.capability_fingerprint.clone(),
        binding_kind: match mapping.disposition {
            MappingDisposition::MappingPolicy => ManifestBindingKind::MappingPolicy,
            MappingDisposition::IdiomaticEquivalent => ManifestBindingKind::IdiomaticEquivalent,
        },
        wire_coordinate: None,
        rust_symbol: None,
        policy_id: Some(mapping.policy_id.clone()),
        decision_id: mapping.decision_id.clone(),
        implementation_fingerprint,
        required_evidence: mapping.required_evidence.clone(),
    })
}

fn expand_mappings<'a>(
    active: &BTreeMap<&CapabilityId, &CapabilityRecord>,
    mappings: &'a CoreCodegenMappings,
    diagnostics: &mut DiagnosticCollector,
) -> BTreeMap<CapabilityId, &'a ReviewedMappingRule> {
    let mut expanded = BTreeMap::new();
    for (policy_id, rule) in &mappings.rules {
        if policy_id != &rule.policy_id
            || rule.expected_count != rule.capability_ids.len()
            || rule.capability_ids.is_empty()
            || rule.authority_id.as_str() == "engine-schema"
        {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityMappingInvalid,
                policy_id,
                "mapping rule identity, count, or authority boundary is invalid",
            ));
        }
        if rule.required_evidence.is_empty() {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityEvidenceIncomplete,
                policy_id,
                "mapping rule declares no executable evidence domains",
            ));
        }
        match rule.disposition {
            MappingDisposition::MappingPolicy if rule.decision_id.is_some() => {
                diagnostics.push(binding_diagnostic(
                    DiagnosticCode::DecisionEvidenceInvalid,
                    policy_id,
                    "a direct mapping policy cannot carry an idiomatic-equivalence decision",
                ))
            }
            MappingDisposition::IdiomaticEquivalent
                if !rule
                    .decision_id
                    .as_ref()
                    .is_some_and(|decision| mappings.decisions.contains(decision)) =>
            {
                diagnostics.push(binding_diagnostic(
                    DiagnosticCode::DecisionEvidenceInvalid,
                    policy_id,
                    "idiomatic mapping rule lacks a decision from the reviewed closed set",
                ));
            }
            MappingDisposition::MappingPolicy | MappingDisposition::IdiomaticEquivalent => {}
        }

        let mut fingerprints = Vec::new();
        for capability_id in rule.capability_ids.iter() {
            match active.get(capability_id) {
                Some(row)
                    if row.authority_id == rule.authority_id
                        && row.capability_kind == rule.capability_kind =>
                {
                    fingerprints.push((capability_id.clone(), row.capability_fingerprint.clone()));
                }
                Some(_) => diagnostics.push(binding_diagnostic(
                    DiagnosticCode::CapabilityMappingInvalid,
                    capability_id,
                    "mapping rule selected a wrong-authority or wrong-kind capability",
                )),
                None => diagnostics.push(binding_diagnostic(
                    DiagnosticCode::CapabilityMappingInvalid,
                    capability_id,
                    "mapping rule selected an inactive or wrong-owner capability",
                )),
            }
            if expanded.insert(capability_id.clone(), rule).is_some() {
                diagnostics.push(binding_diagnostic(
                    DiagnosticCode::CapabilityBindingDuplicate,
                    capability_id,
                    "capability appears in more than one compatibility mapping rule",
                ));
            }
        }
        let fingerprint_digest = serde_json::to_vec(&fingerprints)
            .map(Digest::sha256)
            .unwrap_or_else(|_| Digest::sha256([]));
        if fingerprint_digest != rule.capability_fingerprints_digest {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityFingerprintMismatch,
                policy_id,
                "mapping rule semantic fingerprints differ from the reviewed expansion",
            ));
        }
    }
    expanded
}

fn binding_from_catalog(
    row: &CapabilityRecord,
    key: &BindingKey,
    descriptor: &BindingDescriptor,
) -> BindingRecord {
    let binding_kind = match (descriptor.disposition, key.binding_kind, &key.rust_symbol) {
        (CatalogDisposition::Emitted, _, Some(_)) => ManifestBindingKind::PublicSymbol,
        (CatalogDisposition::RuntimeProvided, _, _) => ManifestBindingKind::RuntimeStrategy,
        (CatalogDisposition::PolicyRecorded, CatalogBindingKind::DirectivePolicy, _) => {
            ManifestBindingKind::TargetInactiveDirective
        }
        (CatalogDisposition::PolicyRecorded, _, _) => ManifestBindingKind::MappingPolicy,
        (CatalogDisposition::Emitted, _, None) => ManifestBindingKind::RuntimeStrategy,
    };
    let policy_id = key
        .rust_symbol
        .is_none()
        .then(|| catalog_policy_id(key.binding_kind));
    BindingRecord {
        capability_id: row.capability_id.clone(),
        authority_id: row.authority_id.clone(),
        capability_fingerprint: row.capability_fingerprint.clone(),
        binding_kind,
        wire_coordinate: key
            .wire_coordinate
            .as_ref()
            .map(|coordinate| coordinate.as_str().to_owned()),
        rust_symbol: key.rust_symbol.clone(),
        policy_id,
        decision_id: None,
        implementation_fingerprint: Digest::new(
            descriptor.implementation_fingerprint.as_str().to_owned(),
        )
        .expect("catalog semantic digests use the shared canonical spelling"),
        required_evidence: required_evidence(key.binding_kind),
    }
}

fn catalog_policy_id(kind: CatalogBindingKind) -> PolicyId {
    let value = match kind {
        CatalogBindingKind::Scalar => "policy/core-codegen/runtime-scalar",
        CatalogBindingKind::TargetPrivateType => "policy/core-codegen/target-private-type",
        CatalogBindingKind::TargetPrivateField => "policy/core-codegen/target-private-field",
        CatalogBindingKind::DirectivePolicy => "policy/core-codegen/target-directive",
        CatalogBindingKind::DirectiveArgument => "policy/core-codegen/directive-argument",
        CatalogBindingKind::QueryRoot
        | CatalogBindingKind::ObjectHandle
        | CatalogBindingKind::InterfaceTrait
        | CatalogBindingKind::InterfaceClient
        | CatalogBindingKind::InterfaceImplementation
        | CatalogBindingKind::Enum
        | CatalogBindingKind::EnumVariant
        | CatalogBindingKind::EnumAlias
        | CatalogBindingKind::InputObject
        | CatalogBindingKind::InputField
        | CatalogBindingKind::FieldOperation
        | CatalogBindingKind::FieldOptions
        | CatalogBindingKind::Argument => "policy/core-codegen/private-runtime-strategy",
    };
    PolicyId::new(value).expect("static catalog policy IDs must be canonical")
}

fn required_evidence(kind: CatalogBindingKind) -> BTreeSet<EvidenceDomain> {
    use EvidenceDomain::{
        Compile, Documentation, ExactTarget, Implementation, Property, QueryProjection,
    };
    match kind {
        CatalogBindingKind::QueryRoot => [Implementation, Compile, Documentation].into(),
        CatalogBindingKind::Scalar
        | CatalogBindingKind::Enum
        | CatalogBindingKind::EnumVariant
        | CatalogBindingKind::InputObject
        | CatalogBindingKind::InputField
        | CatalogBindingKind::FieldOptions => [
            Implementation,
            Property,
            Compile,
            Documentation,
            ExactTarget,
        ]
        .into(),
        CatalogBindingKind::ObjectHandle
        | CatalogBindingKind::InterfaceTrait
        | CatalogBindingKind::InterfaceClient
        | CatalogBindingKind::InterfaceImplementation => {
            [Implementation, Compile, Documentation, ExactTarget].into()
        }
        CatalogBindingKind::FieldOperation => {
            [Implementation, QueryProjection, Documentation, ExactTarget].into()
        }
        CatalogBindingKind::Argument => [Implementation, Property, Compile, QueryProjection].into(),
        CatalogBindingKind::EnumAlias
        | CatalogBindingKind::TargetPrivateType
        | CatalogBindingKind::TargetPrivateField
        | CatalogBindingKind::DirectivePolicy
        | CatalogBindingKind::DirectiveArgument => [Implementation, Property].into(),
    }
}

fn schema_coordinate(
    row: &CapabilityRecord,
) -> Result<(SchemaCoordinate, BTreeSet<CatalogBindingKind>), &'static str> {
    let signature = &row.semantic_signature;
    let text = |name: &str| {
        signature
            .get(name)
            .and_then(serde_json::Value::as_str)
            .ok_or("schema semantic signature is missing a required name")
    };
    let name = |value: &str| {
        SchemaName::try_from(value)
            .map_err(|()| "schema semantic signature contains an invalid GraphQL name")
    };

    match row.capability_kind.as_str() {
        "schema-root" => Ok((
            SchemaCoordinate::query_root(),
            [CatalogBindingKind::QueryRoot].into(),
        )),
        "schema-type" => {
            let wire_name = name(text("name")?)?;
            let kind = text("kind")?;
            let binding = if wire_name.as_str().starts_with('_') {
                CatalogBindingKind::TargetPrivateType
            } else {
                match kind {
                    "SCALAR" => CatalogBindingKind::Scalar,
                    "OBJECT" => CatalogBindingKind::ObjectHandle,
                    "INTERFACE" => CatalogBindingKind::InterfaceTrait,
                    "ENUM" => CatalogBindingKind::Enum,
                    "INPUT_OBJECT" => CatalogBindingKind::InputObject,
                    _ => return Err("schema type kind has no reviewed binding policy"),
                }
            };
            Ok((SchemaCoordinate::named_type(&wire_name), [binding].into()))
        }
        "schema-field" => {
            let parent = name(text("parent")?)?;
            let field = name(text("name")?)?;
            Ok((
                SchemaCoordinate::field(&parent, &field),
                [
                    CatalogBindingKind::FieldOperation,
                    CatalogBindingKind::TargetPrivateField,
                ]
                .into(),
            ))
        }
        "schema-argument" => {
            let parent = signature
                .get("parent")
                .and_then(serde_json::Value::as_object)
                .ok_or("schema argument signature has no parent")?;
            let parent_type = name(
                parent
                    .get("parent_type")
                    .and_then(serde_json::Value::as_str)
                    .ok_or("schema argument signature has no parent type")?,
            )?;
            let parent_field = name(
                parent
                    .get("parent_field")
                    .and_then(serde_json::Value::as_str)
                    .ok_or("schema argument signature has no parent field")?,
            )?;
            let argument = name(text("name")?)?;
            Ok((
                SchemaCoordinate::argument(&parent_type, &parent_field, &argument),
                [CatalogBindingKind::Argument].into(),
            ))
        }
        "schema-input-field" => {
            let parent = signature
                .pointer("/parent/parent_input")
                .and_then(serde_json::Value::as_str)
                .ok_or("schema input field signature has no parent")?;
            let parent = name(parent)?;
            let field = name(text("name")?)?;
            Ok((
                SchemaCoordinate::input_field(&parent, &field),
                [CatalogBindingKind::InputField].into(),
            ))
        }
        "schema-enum-value" => {
            let parent = name(
                signature
                    .get("parent_enum")
                    .and_then(serde_json::Value::as_str)
                    .ok_or("schema enum value signature has no parent")?,
            )?;
            let value = name(text("name")?)?;
            Ok((
                SchemaCoordinate::enum_value(&parent, &value),
                [
                    CatalogBindingKind::EnumVariant,
                    CatalogBindingKind::EnumAlias,
                ]
                .into(),
            ))
        }
        "schema-directive" => {
            let directive = name(text("name")?)?;
            Ok((
                SchemaCoordinate::directive(&directive),
                [CatalogBindingKind::DirectivePolicy].into(),
            ))
        }
        "schema-directive-argument" => {
            let parent = signature
                .pointer("/parent/parent_directive")
                .and_then(serde_json::Value::as_str)
                .ok_or("schema directive argument signature has no parent")?;
            let directive = name(parent)?;
            let argument = name(text("name")?)?;
            Ok((
                SchemaCoordinate::directive_argument(&directive, &argument),
                [CatalogBindingKind::DirectiveArgument].into(),
            ))
        }
        _ => Err("engine-schema capability kind has no reviewed coordinate mapping"),
    }
}

fn validate_binding_bijection(
    ledger: &ResolvedLedger,
    manifest: &GeneratedBindingManifest,
    diagnostics: &mut DiagnosticCollector,
) {
    let expected = ledger
        .capabilities
        .iter()
        .filter(|(_, row)| is_core_codegen_capability(row))
        .map(|(capability_id, _)| capability_id.clone())
        .collect::<BTreeSet<_>>();
    let actual = manifest.bindings.keys().cloned().collect::<BTreeSet<_>>();
    report_set_difference(&expected, &actual, "binding manifest", diagnostics);
    for (capability_id, record) in &manifest.bindings {
        let Some(row) = ledger.capabilities.get(capability_id) else {
            continue;
        };
        if record.capability_id != *capability_id
            || record.authority_id != row.authority_id
            || record.capability_fingerprint != row.capability_fingerprint
        {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityFingerprintMismatch,
                capability_id,
                "binding record identity differs from its authoritative ledger row",
            ));
        }
        if record.required_evidence.is_empty() {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityEvidenceIncomplete,
                capability_id,
                "binding record declares no evidence domains",
            ));
        }
        let has_symbol = record.rust_symbol.is_some();
        let has_policy = record.policy_id.is_some();
        if has_symbol == has_policy {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::CapabilityMappingInvalid,
                capability_id,
                "binding must name exactly one public symbol or mapping policy",
            ));
        }
        if record.binding_kind == ManifestBindingKind::IdiomaticEquivalent
            && record.decision_id.is_none()
        {
            diagnostics.push(binding_diagnostic(
                DiagnosticCode::DecisionEvidenceInvalid,
                capability_id,
                "idiomatic-equivalent binding lacks a reviewed decision",
            ));
        }
    }
}

fn is_core_codegen_capability(row: &CapabilityRecord) -> bool {
    if row.owner_feature == Some(FeatureId::Feature4) {
        return true;
    }

    // Completed rows no longer retain a downstream owner. The exact paired evidence
    // identities preserve their reviewed generator scope so later check/update runs
    // rebuild the same manifest instead of silently dropping already-closed bindings.
    row.status == Status::Implemented
        && row
            .implementation_evidence
            .iter()
            .any(|evidence| evidence.as_str() == GENERATED_CLIENT_IMPLEMENTATION_EVIDENCE)
        && row
            .verification_evidence
            .iter()
            .any(|evidence| evidence.as_str() == GENERATED_CLIENT_VERIFICATION_EVIDENCE)
}

fn report_set_difference(
    expected: &BTreeSet<CapabilityId>,
    actual: &BTreeSet<CapabilityId>,
    subject: &str,
    diagnostics: &mut DiagnosticCollector,
) {
    for capability_id in expected.difference(actual) {
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::CapabilityBindingMissing,
            capability_id,
            format!("{subject} is missing this active capability"),
        ));
    }
    for capability_id in actual.difference(expected) {
        diagnostics.push(binding_diagnostic(
            DiagnosticCode::CapabilityMappingInvalid,
            capability_id,
            format!("{subject} contains an extra or wrong-owner capability"),
        ));
    }
}

fn binding_diagnostic(
    code: DiagnosticCode,
    subject: impl ToString,
    detail: impl Into<String>,
) -> ContractDiagnostic {
    ContractDiagnostic::new(code, subject.to_string(), None, detail)
}
