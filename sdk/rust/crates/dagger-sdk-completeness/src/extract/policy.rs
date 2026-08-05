//! Extraction of reviewed handoff rows and Rust policy clauses.
//!
//! Markdown remains human-authored authority, but selected identities are parsed strictly:
//! malformed table rows and stale exact policy selections fail rather than disappearing silently.

use std::collections::{BTreeMap, BTreeSet};

use serde_json::json;

use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::inventory::{encode_identity_segment, semantic_fingerprint};
use crate::model::{
    AuthorityId, CommitSha, SourceItem, SourceItemId, SourceItemInventory, SourceItemKind,
    SourceItemState, SourceLocator,
};

#[derive(Clone, Debug, Eq, PartialEq)]
/// Exact reviewed prose selected as one Rust policy capability source.
pub struct PolicyClauseSelection {
    /// Stable reviewed semantic coordinate, independent of the prose's source line.
    pub clause_id: String,
    /// Exact text required in the selected Markdown source.
    pub exact_text: String,
}

/// Extracts every removed-test row and binds it to the full recovery commit in the header.
pub fn extract_test_handoff(
    authority_id: &AuthorityId,
    markdown: &str,
    expected_recovery_commit: &CommitSha,
) -> Validation<SourceItemInventory> {
    let mut diagnostics = DiagnosticCollector::default();
    let commits = markdown
        .lines()
        .filter_map(|line| line.trim().strip_prefix("source commit:").map(str::trim))
        .collect::<Vec<_>>();
    if commits != [expected_recovery_commit.as_str()] {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::AuthorityDrift,
            "future/sdk-tests.md",
            None,
            "handoff must declare exactly the selected full recovery commit",
        ));
    }

    let mut items = BTreeMap::new();
    let mut in_inventory = false;
    let mut saw_header = false;
    for (line_index, line) in markdown.lines().enumerate() {
        if line.trim() == "## Inventory" {
            in_inventory = true;
            continue;
        }
        if in_inventory && line.starts_with("## ") {
            break;
        }
        if !in_inventory || !line.trim_start().starts_with('|') {
            continue;
        }
        let columns = line
            .trim()
            .trim_matches('|')
            .split('|')
            .map(|column| column.trim())
            .collect::<Vec<_>>();
        if columns.first() == Some(&"Test") {
            saw_header = columns
                == [
                    "Test",
                    "Recover from",
                    "Ultra-TLDR purpose",
                    "Ultra-TLDR SDK home",
                ];
            continue;
        }
        if columns
            .iter()
            .all(|column| column.chars().all(|value| value == '-'))
        {
            continue;
        }
        if columns.len() != 4 || !saw_header {
            diagnostics.push(policy_error(
                format!("future/sdk-tests.md:{}", line_index + 1),
                "handoff inventory row does not match the approved four-column format",
            ));
            continue;
        }
        let test = strip_code(columns[0]);
        let recovery = strip_code(columns[1]);
        let Some((short_commit, recovery_path)) = recovery.split_once(':') else {
            diagnostics.push(policy_error(
                test,
                "handoff recovery locator has no commit prefix",
            ));
            continue;
        };
        if !expected_recovery_commit.as_str().starts_with(short_commit)
            || short_commit.len() < 9
            || recovery_path.is_empty()
        {
            diagnostics.push(policy_error(
                test,
                "handoff recovery locator does not resolve beneath the full header commit",
            ));
            continue;
        }
        let identity = format!(
            "source/{authority_id}/removed-test/{}",
            encode_identity_segment(test)
        );
        let (Ok(source_item_id), Ok(kind), Ok(locator)) = (
            SourceItemId::new(identity),
            SourceItemKind::new("removed-test"),
            SourceLocator::new(format!("future/sdk-tests.md:{}", line_index + 1)),
        ) else {
            diagnostics.push(policy_error(
                test,
                "handoff row identity or locator is invalid",
            ));
            continue;
        };
        let signature = json!({
            "test": test,
            "recovery_commit": expected_recovery_commit,
            "recovery_path": recovery_path,
            "purpose": columns[2],
            "sdk_home": columns[3],
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
            item_kind: kind,
            locator,
            semantic_signature: signature,
            fingerprint,
            state: SourceItemState::Removed,
        };
        if items.insert(source_item_id.clone(), item).is_some() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityDuplicate,
                source_item_id.to_string(),
                None,
                "handoff table contains a duplicate removed-test identity",
            ));
        }
    }
    if items.is_empty() {
        diagnostics.push(policy_error(
            "future/sdk-tests.md",
            "handoff inventory contains no removed tests",
        ));
    }
    diagnostics.finish(SourceItemInventory { items })
}

/// Verifies and extracts an exhaustive reviewed set of exact Rust policy clauses.
pub fn extract_policy_clauses(
    authority_id: &AuthorityId,
    path: &str,
    markdown: &str,
    selections: &[PolicyClauseSelection],
) -> Validation<SourceItemInventory> {
    let mut diagnostics = DiagnosticCollector::default();
    if selections.is_empty() {
        diagnostics.push(policy_error(
            path,
            "Rust policy extraction requires a non-empty reviewed clause set",
        ));
    }
    let mut seen_ids = BTreeSet::new();
    let mut items = BTreeMap::new();
    for selection in selections {
        let occurrences = markdown
            .match_indices(&selection.exact_text)
            .collect::<Vec<_>>();
        if selection.clause_id.is_empty()
            || selection.exact_text.trim() != selection.exact_text
            || selection.exact_text.is_empty()
            || !seen_ids.insert(&selection.clause_id)
            || occurrences.len() != 1
        {
            diagnostics.push(policy_error(
                &selection.clause_id,
                "reviewed policy clause must have one stable ID and one exact source occurrence",
            ));
            continue;
        }
        let byte = occurrences[0].0;
        let identity = format!(
            "source/{authority_id}/rust-policy/{}",
            encode_identity_segment(&selection.clause_id)
        );
        let (Ok(source_item_id), Ok(kind), Ok(locator)) = (
            SourceItemId::new(identity),
            SourceItemKind::new("rust-policy"),
            SourceLocator::new(format!(
                "{path}:{}",
                markdown[..byte]
                    .bytes()
                    .filter(|value| *value == b'\n')
                    .count()
                    + 1
            )),
        ) else {
            diagnostics.push(policy_error(
                &selection.clause_id,
                "policy identity or locator is invalid",
            ));
            continue;
        };
        let signature = json!({
            "clause_id": selection.clause_id,
            "text": selection.exact_text,
        });
        let fingerprint = match semantic_fingerprint(&signature) {
            Ok(fingerprint) => fingerprint,
            Err(errors) => {
                diagnostics.extend(errors.into_inner());
                continue;
            }
        };
        items.insert(
            source_item_id.clone(),
            SourceItem {
                source_item_id,
                authority_id: authority_id.clone(),
                item_kind: kind,
                locator,
                semantic_signature: signature,
                fingerprint,
                state: SourceItemState::Active,
            },
        );
    }
    diagnostics.finish(SourceItemInventory { items })
}

/// Merges independently extracted authority inventories without hiding identity collisions.
pub fn merge_source_inventories(
    inventories: impl IntoIterator<Item = SourceItemInventory>,
) -> Validation<SourceItemInventory> {
    let mut diagnostics = DiagnosticCollector::default();
    let mut items = BTreeMap::new();
    for inventory in inventories {
        for (source_item_id, item) in inventory.items {
            if items.insert(source_item_id.clone(), item).is_some() {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::CapabilityDuplicate,
                    source_item_id.to_string(),
                    None,
                    "independent extractors produced the same source-item identity",
                ));
            }
        }
    }
    diagnostics.finish(SourceItemInventory { items })
}

fn strip_code(value: &str) -> &str {
    value
        .strip_prefix('`')
        .and_then(|value| value.strip_suffix('`'))
        .unwrap_or(value)
}

fn policy_error(subject: impl Into<String>, detail: impl Into<String>) -> ContractDiagnostic {
    ContractDiagnostic::new(
        DiagnosticCode::CapabilitySignatureInvalid,
        subject,
        None,
        detail,
    )
}
