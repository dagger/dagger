//! Stable capability identity, semantic fingerprinting, and authority precedence.
//!
//! Identity is derived from reviewed semantic coordinates, never from source locations. The
//! builder keeps every corroborating source item while rejecting conflicts that peer precedence
//! cannot resolve explicitly.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::authority::{SourceItemDisposition, ValidatedSourceCoverage};
use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, Validation,
};
use crate::model::{
    CanonicalInventory, CanonicalSet, CapabilityDefinition, CapabilityId, CapabilityKind, Digest,
    NonEmptyText, SourceItemInventory, SourceItemState, Stability,
};

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Authority role used when resolving definitions of a shared behavioural capability.
pub enum CapabilityOrigin {
    /// Engine-schema or non-peer reviewed authority.
    Independent,
    /// Go SDK declaration or behaviour used as a reference for common lifecycle contracts.
    Go,
    /// Pinned language-neutral SDK contract harness.
    Harness,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// One reviewed candidate definition before overlap resolution.
pub struct CapabilityCandidate {
    /// Complete proposed capability definition.
    pub definition: CapabilityDefinition,
    /// Peer authority role of the proposal.
    pub origin: CapabilityOrigin,
    /// Whether the proposal defines a common lifecycle contract subject to peer precedence.
    pub common_contract: bool,
    /// Whether a harness proposal selects the same CLI and engine as the immutable target.
    pub target_compatible: bool,
}

/// Percent-encodes one semantic coordinate segment without losing its original spelling.
///
/// Lowercase ASCII letters, digits, `-`, `_`, and `.` remain readable. Every other UTF-8 byte is
/// encoded with uppercase hexadecimal so the result is accepted by durable canonical IDs.
pub fn encode_identity_segment(segment: &str) -> String {
    const HEX: &[u8; 16] = b"0123456789ABCDEF";
    let mut encoded = String::with_capacity(segment.len());
    for byte in segment.bytes() {
        if byte.is_ascii_lowercase() || byte.is_ascii_digit() || matches!(byte, b'-' | b'_' | b'.')
        {
            encoded.push(char::from(byte));
        } else {
            encoded.push('%');
            encoded.push(char::from(HEX[usize::from(byte >> 4)]));
            encoded.push(char::from(HEX[usize::from(byte & 0x0f)]));
        }
    }
    encoded
}

/// Reverses [`encode_identity_segment`], rejecting malformed or non-canonical encodings.
pub fn decode_identity_segment(encoded: &str) -> Result<String, &'static str> {
    let bytes = encoded.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] != b'%' {
            if !(bytes[index].is_ascii_lowercase()
                || bytes[index].is_ascii_digit()
                || matches!(bytes[index], b'-' | b'_' | b'.'))
            {
                return Err("identity segment contains a non-canonical literal byte");
            }
            decoded.push(bytes[index]);
            index += 1;
            continue;
        }
        if index + 2 >= bytes.len() {
            return Err("identity segment ends inside a percent escape");
        }
        let high = decode_hex(bytes[index + 1])?;
        let low = decode_hex(bytes[index + 2])?;
        decoded.push((high << 4) | low);
        index += 3;
    }
    let decoded = String::from_utf8(decoded).map_err(|_| "identity segment is not UTF-8")?;
    if encode_identity_segment(&decoded) != encoded {
        return Err("identity segment is not canonically encoded");
    }
    Ok(decoded)
}

/// Constructs a stable schema identity from its atomic kind and canonical schema coordinate.
pub fn schema_capability_id(
    authority: &crate::model::AuthorityId,
    schema_kind: &str,
    coordinate: &[&str],
) -> Result<CapabilityId, crate::model::ValueError> {
    capability_id("schema", authority.as_str(), Some(schema_kind), coordinate)
}

/// Constructs a stable reviewed behavioural identity from its semantic coordinate.
pub fn behavior_capability_id(
    authority: &crate::model::AuthorityId,
    coordinate: &[&str],
) -> Result<CapabilityId, crate::model::ValueError> {
    capability_id("behavior", authority.as_str(), None, coordinate)
}

/// Constructs a stable reviewed Rust-policy identity from its semantic coordinate.
pub fn policy_capability_id(
    authority: &crate::model::AuthorityId,
    coordinate: &[&str],
) -> Result<CapabilityId, crate::model::ValueError> {
    capability_id("policy", authority.as_str(), None, coordinate)
}

fn capability_id(
    namespace: &str,
    authority: &str,
    kind: Option<&str>,
    coordinate: &[&str],
) -> Result<CapabilityId, crate::model::ValueError> {
    let mut segments = vec![namespace.to_owned(), authority.to_owned()];
    if let Some(kind) = kind {
        segments.push(kind.to_owned());
    }
    segments.extend(
        coordinate
            .iter()
            .map(|segment| encode_identity_segment(segment)),
    );
    CapabilityId::new(segments.join("/"))
}

/// Computes the domain-separated fingerprint of a normalized semantic signature.
pub fn semantic_fingerprint(signature: &serde_json::Value) -> Result<Digest, DiagnosticSet> {
    canonical_digest(DigestDomain::Capability, signature).map_err(|_| {
        DiagnosticSet::new([ContractDiagnostic::new(
            DiagnosticCode::CapabilitySignatureInvalid,
            "semantic-signature",
            None,
            "semantic signature could not be canonically encoded",
        )])
        .expect("one diagnostic always forms a non-empty set")
    })
}

/// Derives one atomic capability candidate for every extracted schema source item.
///
/// Schema coordinates are decoded from the extractor's stable source identity and then encoded
/// again by the capability constructor. This round trip prevents a pre-escaped coordinate from
/// being double encoded or smuggling a non-canonical identity into the inventory.
pub fn derive_schema_candidates(
    source_items: &SourceItemInventory,
) -> Validation<Vec<CapabilityCandidate>> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut candidates = Vec::new();
    for item in source_items
        .items
        .values()
        .filter(|item| item.item_kind.as_str().starts_with("schema-"))
    {
        let kind = item.item_kind.as_str();
        let prefix = format!("source/{}/{kind}/", item.authority_id);
        let Some(encoded_coordinate) = item.source_item_id.as_str().strip_prefix(&prefix) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySignatureInvalid,
                item.source_item_id.to_string(),
                Some(item.locator.clone()),
                "schema source identity does not contain its authority and atomic kind",
            ));
            continue;
        };
        let decoded = encoded_coordinate
            .split('/')
            .map(decode_identity_segment)
            .collect::<Result<Vec<_>, _>>();
        let Ok(decoded) = decoded else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySignatureInvalid,
                item.source_item_id.to_string(),
                Some(item.locator.clone()),
                "schema source coordinate is not reversibly percent encoded",
            ));
            continue;
        };
        let coordinate = decoded.iter().map(String::as_str).collect::<Vec<_>>();
        let (Ok(capability_id), Ok(capability_kind), Ok(summary)) = (
            schema_capability_id(&item.authority_id, kind, &coordinate),
            CapabilityKind::new(kind),
            NonEmptyText::new(format!("Schema element {}", item.locator)),
        ) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySignatureInvalid,
                item.source_item_id.to_string(),
                Some(item.locator.clone()),
                "schema item could not form a durable capability definition",
            ));
            continue;
        };
        let fingerprint = match semantic_fingerprint(&item.semantic_signature) {
            Ok(fingerprint) => fingerprint,
            Err(errors) => {
                diagnostics.extend(errors.into_inner());
                continue;
            }
        };
        candidates.push(CapabilityCandidate {
            definition: CapabilityDefinition {
                capability_id,
                authority_id: item.authority_id.clone(),
                capability_kind,
                source_item_ids: CanonicalSet::new([item.source_item_id.clone()]),
                source_anchors: CanonicalSet::default(),
                summary,
                semantic_signature: item.semantic_signature.clone(),
                capability_fingerprint: fingerprint,
                stability: Stability::Stable,
            },
            origin: CapabilityOrigin::Independent,
            common_contract: false,
            target_compatible: true,
        });
    }
    diagnostics.finish(candidates)
}

/// Builds an exhaustive canonical inventory and applies explicit Go/harness peer precedence.
///
/// Coverage is checked a second time at this boundary: each primary/reference assignment must
/// name a produced capability, and non-passing source states may corroborate but never become a
/// primary definition. This prevents skipped or historical tests from becoming completion claims.
pub fn build_inventory(
    source_items: &SourceItemInventory,
    coverage: &ValidatedSourceCoverage,
    candidates: impl IntoIterator<Item = CapabilityCandidate>,
) -> Validation<CanonicalInventory> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut grouped = BTreeMap::<CapabilityId, Vec<CapabilityCandidate>>::new();
    let mut candidates = candidates.into_iter().collect::<Vec<_>>();
    match derive_schema_candidates(source_items) {
        Ok(schema_candidates) => candidates.extend(schema_candidates),
        Err(errors) => diagnostics.extend(errors.into_inner()),
    }
    for candidate in candidates {
        validate_candidate(&candidate, source_items, &mut diagnostics);
        grouped
            .entry(candidate.definition.capability_id.clone())
            .or_default()
            .push(candidate);
    }

    let mut capabilities = BTreeMap::new();
    for (capability_id, candidates) in grouped {
        if let Some(definition) = resolve_candidates(&capability_id, candidates, &mut diagnostics) {
            capabilities.insert(capability_id, definition);
        }
    }

    for (source_item_id, assignment) in &coverage.as_inner().items {
        match &assignment.disposition {
            SourceItemDisposition::Excluded(_) => {}
            SourceItemDisposition::Primary(ids) | SourceItemDisposition::Reference(ids) => {
                for capability_id in ids.as_slice() {
                    let Some(definition) = capabilities.get(capability_id) else {
                        diagnostics.push(ContractDiagnostic::new(
                            DiagnosticCode::CapabilitySourceMissing,
                            capability_id.to_string(),
                            None,
                            "source coverage names a capability that no definition produced",
                        ));
                        continue;
                    };
                    if !definition
                        .source_item_ids
                        .as_slice()
                        .contains(source_item_id)
                    {
                        diagnostics.push(ContractDiagnostic::new(
                            DiagnosticCode::CapabilitySourceMissing,
                            capability_id.to_string(),
                            None,
                            "capability definition omits a source item assigned to it",
                        ));
                    }
                }
            }
        }
    }

    for definition in capabilities.values() {
        validate_definition(definition, source_items, coverage, &mut diagnostics);
    }
    let has_schema_authority = capabilities
        .keys()
        .any(|capability_id| capability_id.as_str().starts_with("schema/"));
    for assignment in coverage.as_inner().items.values() {
        if let SourceItemDisposition::Excluded(exclusion) = &assignment.disposition {
            // Generated bindings are redundant only when this same construction has retained the
            // engine schema that defines them. A rationale alone cannot establish that backing.
            if exclusion.rationale.as_str() == "represented-by-engine-schema"
                && !has_schema_authority
            {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::AuthorityExclusionInvalid,
                    assignment.source_item_id.to_string(),
                    None,
                    "generated-binding exclusion has no schema-backed capability inventory",
                ));
            }
        }
    }

    diagnostics.finish(CanonicalInventory { capabilities })
}

fn resolve_candidates(
    capability_id: &CapabilityId,
    candidates: Vec<CapabilityCandidate>,
    diagnostics: &mut DiagnosticCollector,
) -> Option<CapabilityDefinition> {
    let incompatible_harness = candidates.iter().any(|candidate| {
        candidate.origin == CapabilityOrigin::Harness && !candidate.target_compatible
    });
    if incompatible_harness {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::SdkContractTargetMismatch,
            capability_id.to_string(),
            None,
            "harness definition does not select the immutable target CLI and engine",
        ));
        return None;
    }

    let common_peer = candidates.iter().all(|candidate| candidate.common_contract)
        && candidates.iter().all(|candidate| {
            matches!(
                candidate.origin,
                CapabilityOrigin::Go | CapabilityOrigin::Harness
            )
        })
        && candidates
            .iter()
            .any(|candidate| candidate.origin == CapabilityOrigin::Harness)
        && candidates
            .iter()
            .any(|candidate| candidate.origin == CapabilityOrigin::Go);
    let primary = if common_peer {
        unique_origin_candidate(
            capability_id,
            &candidates,
            CapabilityOrigin::Go,
            diagnostics,
        )?;
        unique_origin_candidate(
            capability_id,
            &candidates,
            CapabilityOrigin::Harness,
            diagnostics,
        )?
    } else {
        let first = candidates.first()?;
        if candidates.iter().any(|candidate| {
            candidate.definition.semantic_signature != first.definition.semantic_signature
                || candidate.definition.authority_id != first.definition.authority_id
        }) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityDuplicate,
                capability_id.to_string(),
                None,
                "one capability identity has competing primary semantic definitions",
            ));
            return None;
        }
        first
    };

    let mut definition = primary.definition.clone();
    let source_ids = candidates
        .iter()
        .flat_map(|candidate| {
            candidate
                .definition
                .source_item_ids
                .as_slice()
                .iter()
                .cloned()
        })
        .collect::<BTreeSet<_>>();
    definition.source_item_ids = CanonicalSet::new(source_ids);
    definition.source_anchors = CanonicalSet::new(
        candidates
            .iter()
            .flat_map(|candidate| {
                candidate
                    .definition
                    .source_anchors
                    .as_slice()
                    .iter()
                    .cloned()
            })
            .collect::<BTreeSet<_>>(),
    );
    Some(definition)
}

fn validate_candidate(
    candidate: &CapabilityCandidate,
    source_items: &SourceItemInventory,
    diagnostics: &mut DiagnosticCollector,
) {
    match semantic_fingerprint(&candidate.definition.semantic_signature) {
        Ok(fingerprint) if fingerprint == candidate.definition.capability_fingerprint => {}
        Ok(_) | Err(_) => diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityFingerprintMismatch,
            candidate.definition.capability_id.to_string(),
            None,
            "candidate fingerprint differs from its normalized semantic signature",
        )),
    }
    for source_item_id in candidate.definition.source_item_ids.as_slice() {
        match source_items.items.get(source_item_id) {
            Some(item) if item.authority_id == candidate.definition.authority_id => {}
            Some(item) => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityAuthorityMissing,
                candidate.definition.capability_id.to_string(),
                Some(item.locator.clone()),
                "candidate source item belongs to a different authority",
            )),
            None => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                candidate.definition.capability_id.to_string(),
                None,
                "candidate references an unknown source item",
            )),
        }
    }
}

fn unique_origin_candidate<'a>(
    capability_id: &CapabilityId,
    candidates: &'a [CapabilityCandidate],
    origin: CapabilityOrigin,
    diagnostics: &mut DiagnosticCollector,
) -> Option<&'a CapabilityCandidate> {
    let selected = candidates
        .iter()
        .filter(|candidate| candidate.origin == origin)
        .collect::<Vec<_>>();
    let first = *selected.first()?;
    if selected.iter().skip(1).any(|candidate| {
        candidate.definition.semantic_signature != first.definition.semantic_signature
    }) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityDuplicate,
            capability_id.to_string(),
            None,
            "peer authority supplied competing semantic signatures",
        ));
        return None;
    }
    Some(first)
}

fn validate_definition(
    definition: &CapabilityDefinition,
    source_items: &SourceItemInventory,
    coverage: &ValidatedSourceCoverage,
    diagnostics: &mut DiagnosticCollector,
) {
    match semantic_fingerprint(&definition.semantic_signature) {
        Ok(fingerprint) if fingerprint == definition.capability_fingerprint => {}
        Ok(_) | Err(_) => diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityFingerprintMismatch,
            definition.capability_id.to_string(),
            None,
            "capability fingerprint differs from its normalized semantic signature",
        )),
    }

    if definition.source_item_ids.is_empty() {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilitySourceMissing,
            definition.capability_id.to_string(),
            None,
            "capability must retain at least one source item",
        ));
    }
    for source_item_id in definition.source_item_ids.as_slice() {
        let Some(source_item) = source_items.items.get(source_item_id) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                definition.capability_id.to_string(),
                None,
                "capability references an unknown source item",
            ));
            continue;
        };
        if !coverage.as_inner().items.contains_key(source_item_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                definition.capability_id.to_string(),
                Some(source_item.locator.clone()),
                "capability source item has no validated coverage assignment",
            ));
        }
        let primary = coverage.as_inner().items.get(source_item_id).is_some_and(|item| {
            matches!(&item.disposition, SourceItemDisposition::Primary(ids) if ids.as_slice().contains(&definition.capability_id))
        });
        if primary
            && matches!(
                source_item.state,
                SourceItemState::Skipped | SourceItemState::Removed | SourceItemState::HarnessSelf
            )
        {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilitySourceMissing,
                definition.capability_id.to_string(),
                Some(source_item.locator.clone()),
                "non-passing source item cannot be a primary active capability definition",
            ));
        }
    }
}

fn decode_hex(byte: u8) -> Result<u8, &'static str> {
    match byte {
        b'0'..=b'9' => Ok(byte - b'0'),
        b'A'..=b'F' => Ok(byte - b'A' + 10),
        _ => Err("percent escapes must use uppercase hexadecimal"),
    }
}
