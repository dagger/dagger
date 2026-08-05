//! Strict adapter for the standard-library Go authority extractor.
//!
//! The helper is intentionally incapable of constructing durable capability identities. Rust
//! revalidates its direct AST digest and derives a domain-separated semantic fingerprint.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, Validation,
};
use crate::inventory::{encode_identity_segment, semantic_fingerprint};
use crate::model::{
    AuthorityId, Digest, SourceItem, SourceItemId, SourceItemInventory, SourceItemKind,
    SourceItemState, SourceLocator,
};

const GO_HELPER_FORMAT_VERSION: u32 = 1;

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Complete normalized output from the standard-library Go helper.
pub struct GoHelperOutput {
    pub format_version: u32,
    pub items: Vec<GoHelperItem>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub go_sdk_lib_version: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// One helper declaration or test identity awaiting Rust revalidation.
pub struct GoHelperItem {
    pub kind: GoItemKind,
    pub package: String,
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub receiver: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub parent: String,
    pub signature: String,
    pub state: GoItemState,
    pub locator: String,
    pub fingerprint: Digest,
}

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Closed declaration and test kinds emitted by the Go helper.
pub enum GoItemKind {
    Type,
    Const,
    Var,
    Function,
    Method,
    Test,
    Subtest,
    DynamicSubtest,
    TestTable,
}

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Lifecycle state preserved by the Go helper and reviewed handoff fixtures.
pub enum GoItemState {
    Active,
    Deprecated,
    Skipped,
    Removed,
}

/// Decodes and validates helper output, including the engine-selected Go commit literal.
pub fn adapt_go_output(
    authority_id: &AuthorityId,
    bytes: &[u8],
    expected_go_revision: &str,
) -> Validation<SourceItemInventory> {
    adapt_go_output_inner(authority_id, bytes, Some(expected_go_revision))
}

/// Decodes helper output for an authority set that does not own the engine selection literal.
pub fn adapt_go_output_without_version(
    authority_id: &AuthorityId,
    bytes: &[u8],
) -> Validation<SourceItemInventory> {
    adapt_go_output_inner(authority_id, bytes, None)
}

fn adapt_go_output_inner(
    authority_id: &AuthorityId,
    bytes: &[u8],
    expected_go_revision: Option<&str>,
) -> Validation<SourceItemInventory> {
    let output: GoHelperOutput = serde_json::from_slice(bytes)
        .map_err(|error| one_diagnostic(format!("Go helper output is invalid: {error}")))?;
    let mut diagnostics = DiagnosticCollector::default();
    if output.format_version != GO_HELPER_FORMAT_VERSION {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::AuthorityExtractorInvalid,
            authority_id.to_string(),
            None,
            "Go helper format version is unsupported",
        ));
    }
    // The adjacent human version comment is deliberately absent from this protocol. Only the
    // evaluated string literal participates in immutable target selection.
    if output.go_sdk_lib_version.as_deref() != expected_go_revision {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::GoRevisionMismatch,
            authority_id.to_string(),
            None,
            "goSDKLibVersion literal presence or value differs from this authority contract",
        ));
    }

    let mut items = BTreeMap::new();
    for helper in output.items {
        if helper.package.is_empty()
            || helper.name.is_empty()
            || helper.signature.is_empty()
            || helper.fingerprint != Digest::sha256(helper.signature.as_bytes())
        {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityExtractorInvalid,
                helper.name,
                None,
                "Go helper item is empty or its direct normalized-AST digest is invalid",
            ));
            continue;
        }
        let kind = go_kind_name(helper.kind);
        let coordinate = [
            &helper.package,
            &helper.receiver,
            &helper.parent,
            &helper.name,
        ]
        .into_iter()
        .filter(|segment| !segment.is_empty())
        .map(|segment| encode_identity_segment(segment))
        .collect::<Vec<_>>()
        .join("/");
        let identity = format!("source/{authority_id}/go-{kind}/{coordinate}");
        let (Ok(source_item_id), Ok(item_kind), Ok(locator)) = (
            SourceItemId::new(identity),
            SourceItemKind::new(format!("go-{kind}")),
            SourceLocator::new(&helper.locator),
        ) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityExtractorInvalid,
                helper.name,
                None,
                "Go helper item identity, kind, or locator is not portable",
            ));
            continue;
        };
        let signature = json!({
            "kind": helper.kind,
            "package": helper.package,
            "name": helper.name,
            "receiver": helper.receiver,
            "parent": helper.parent,
            "signature": helper.signature,
        });
        let fingerprint = match semantic_fingerprint(&signature) {
            Ok(fingerprint) => fingerprint,
            Err(errors) => {
                diagnostics.extend(errors.into_inner());
                continue;
            }
        };
        let item = SourceItem {
            source_item_id: source_item_id.clone(),
            authority_id: authority_id.clone(),
            item_kind,
            locator,
            semantic_signature: signature,
            fingerprint,
            state: match helper.state {
                GoItemState::Active => SourceItemState::Active,
                GoItemState::Deprecated => SourceItemState::Deprecated,
                GoItemState::Skipped => SourceItemState::Skipped,
                GoItemState::Removed => SourceItemState::Removed,
            },
        };
        if items.insert(source_item_id.clone(), item).is_some() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityDuplicate,
                source_item_id.to_string(),
                None,
                "Go helper output contains a duplicate semantic source identity",
            ));
        }
    }
    diagnostics.finish(SourceItemInventory { items })
}

fn go_kind_name(kind: GoItemKind) -> &'static str {
    match kind {
        GoItemKind::Type => "type",
        GoItemKind::Const => "const",
        GoItemKind::Var => "var",
        GoItemKind::Function => "function",
        GoItemKind::Method => "method",
        GoItemKind::Test => "test",
        GoItemKind::Subtest => "subtest",
        GoItemKind::DynamicSubtest => "dynamic-subtest",
        GoItemKind::TestTable => "test-table",
    }
}

fn one_diagnostic(detail: String) -> DiagnosticSet {
    DiagnosticSet::new([ContractDiagnostic::new(
        DiagnosticCode::AuthorityExtractorInvalid,
        "go-helper",
        None,
        detail,
    )])
    .expect("one diagnostic always forms a non-empty set")
}
